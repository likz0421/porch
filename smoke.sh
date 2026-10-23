#!/bin/sh
# 冒烟测试：验证核心接口可用。用法: sh smoke.sh [端口] [配置目录]
# 依赖: curl, python(仅用于解析 JSON)
PORT=${1:-5200}
DIR=${2:-./porchconf}
BASE="http://127.0.0.1:$PORT"
PY=${PY:-python3}

pass=0
fail=0

check() {
	desc=$1
	cond=$2
	if [ "$cond" = "0" ]; then
		echo "  [PASS] $desc"
		pass=$((pass + 1))
	else
		echo "  [FAIL] $desc"
		fail=$((fail + 1))
	fi
}

echo "=== porch 冒烟测试 (端口 $PORT) ==="

# 1 探活
body=$(curl -s -m 5 "$BASE/api/ping")
echo "$body" | grep -q '"ok":true'
check "探活接口 /api/ping" $?

# 2 登录
TOKEN=$(curl -s -m 5 -X POST "$BASE/api/login" -H 'Content-Type: application/json' \
	-d '{"account":"666","password":"666"}' | $PY -c "import sys,json;print(json.load(sys.stdin)['data']['token'])" 2>/dev/null)
[ -n "$TOKEN" ]
check "登录获取 token" $?

# 3 错误密码应被拒
curl -s -m 5 -X POST "$BASE/api/login" -H 'Content-Type: application/json' \
	-d '{"account":"666","password":"bad"}' | grep -q '"ok":false'
check "错误密码被拒绝" $?

# 4 未登录访问受保护接口应 401
code=$(curl -s -o /dev/null -m 5 -w "%{http_code}" "$BASE/api/state")
[ "$code" = "401" ]
check "未登录访问 /api/state 返回 401 (实际 $code)" $?

# 5 状态接口
curl -s -m 5 "$BASE/api/state" -H "X-Token: $TOKEN" | grep -q '"base"'
check "状态接口 /api/state" $?

# 6 静态资源
code=$(curl -s -o /dev/null -m 5 -w "%{http_code}" "$BASE/")
[ "$code" = "200" ]
check "Web 首页返回 200" $?

# 7 新建 DDNS 任务并执行
KEY=$(curl -s -m 5 -X POST "$BASE/api/ddns" -H "X-Token: $TOKEN" -H 'Content-Type: application/json' \
	-d '{"name":"冒烟测试","enable":true,"provider":"none","domain":"smoke.local","subDomain":"a","recordType":"A","ipType":"ipv4","ipSource":"manual","manualIP":"198.51.100.1","checkInterval":60,"ttl":600}' \
	| $PY -c "import sys,json;print(json.load(sys.stdin)['data']['key'])" 2>/dev/null)
[ -n "$KEY" ]
check "新建 DDNS 任务" $?

curl -s -m 10 -X POST "$BASE/api/ddns/$KEY/run" -H "X-Token: $TOKEN" | grep -q '"ok":true'
check "触发 DDNS 执行" $?

sleep 2
curl -s -m 5 "$BASE/api/ddns" -H "X-Token: $TOKEN" | grep -q '198.51.100.1'
check "DDNS 记录到解析 IP" $?

# 8 Lucky 配置导入（目录内需含 lucky_*.lkcf；没有样本时记为跳过，不算失败）
if ls "$DIR"/lucky_*.lkcf >/dev/null 2>&1; then
	resp=$(curl -s -m 10 -X POST "$BASE/api/import/lucky" -H "X-Token: $TOKEN" \
		-H 'Content-Type: application/json' -d "{\"dir\":\"$DIR\",\"apply\":false}")
	echo "$resp" | grep -q '"ok":true'
	check "Lucky 配置导入预览" $?
else
	echo "  [SKIP] Lucky 配置导入预览（$DIR 内无 lucky_*.lkcf 样本）"
fi

# 9 日志
curl -s -m 5 "$BASE/api/logs?since=0" -H "X-Token: $TOKEN" | grep -q '"seq"'
check "日志接口 /api/logs" $?

# 10 清理测试任务
curl -s -m 5 -X DELETE "$BASE/api/ddns/$KEY" -H "X-Token: $TOKEN" | grep -q '"ok":true'
check "删除测试任务" $?

echo
echo "通过 $pass 项，失败 $fail 项"
[ "$fail" = "0" ]
