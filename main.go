// Porch 管理端主程序：动态域名(DDNS) + SSL 证书(ACME) + 反向代理，内置毛玻璃风格 Web 界面。
//
// 用法:
//
//	porch -cd /etc/porch/porchconf    指定配置目录（不存在时自动创建）
//	porch -p 16601                    临时指定管理端口
//	porch -v                          查看版本
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"porch/internal/cert"
	"porch/internal/config"
	"porch/internal/ddns"
	"porch/internal/logger"
	"porch/internal/metrics"
	"porch/internal/reverseproxy"
	"porch/internal/sysinfo"
	"porch/internal/webapi"
)

var (
	version = "1.1.0"
	build   = "selfbuild"
)

const (
	// defaultConfName 默认配置目录名
	defaultConfName = "porchconf"
	// legacyConfName 旧版（Lucky 时期）配置目录名，升级时自动沿用
	legacyConfName = "luckyconf"
)

// legacyConfDir 目标目录不存在、但同级存在旧版配置目录时返回旧目录，
// 让老用户的 DDNS/证书/反代配置在改名后仍然生效。
func legacyConfDir(target string) string {
	if target == "" || !strings.HasSuffix(filepath.Base(target), defaultConfName) {
		return ""
	}
	if _, err := os.Stat(target); err == nil {
		return "" // 新目录已存在，直接用新的
	}
	legacy := filepath.Join(filepath.Dir(target), legacyConfName)
	if st, err := os.Stat(legacy); err == nil && st.IsDir() {
		return legacy
	}
	return ""
}

func main() {
	confDir := flag.String("cd", "", "配置目录路径（默认当前目录下 porchconf）")
	port := flag.Int("p", 0, "临时指定管理端口，覆盖配置文件")
	openWeb := flag.Bool("open", false, "启动后自动用默认浏览器打开管理页面")
	showVer := flag.Bool("v", false, "显示版本信息")
	flag.Parse()

	if *showVer {
		fmt.Printf("porch %s (%s) %s/%s\n", version, build, sysinfo.Collect().OS, sysinfo.Collect().Arch)
		return
	}
	sysinfo.Version.Store(version)
	initConsole()
	// 启动指标采集：为面板的实时曲线提供数据
	metrics.Start()

	dir := *confDir
	if dir == "" {
		if env := os.Getenv("PORCH_CONF_DIR"); env != "" {
			dir = env
		} else if runtime.GOOS == "windows" {
			// Windows 下默认放在程序所在目录，解压即可用
			if exe, err := os.Executable(); err == nil {
				dir = filepath.Join(filepath.Dir(exe), defaultConfName)
			} else {
				dir = defaultConfName
			}
		} else {
			dir = defaultConfName
		}
	}
	// 老版本配置目录（luckyconf）自动沿用，避免升级后配置"消失"
	if legacy := legacyConfDir(dir); legacy != "" {
		logger.Info("main", "发现旧版配置目录 %s，继续沿用（可手动改名为 %s）", legacy, defaultConfName)
		dir = legacy
	}

	store, err := config.Init(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化配置失败: %v\n", err)
		os.Exit(1)
	}
	setLogLevel(store.Get().Base.LogLevel)

	logger.Info("main", "porch %s 启动中，配置目录: %s", version, store.Dir())

	if *port > 0 && *port <= 65535 {
		store.Update(func(c *config.Config) { c.Base.AdminWebListenPort = *port })
		logger.Info("main", "本次运行使用命令行指定端口: %d", *port)
	}

	// 业务模块
	ddnsMgr := ddns.New(store)
	certMgr := cert.New(store)
	proxyMgr := reverseproxy.New(store)

	// 配置变更时自动重载对应模块
	store.OnChange(func(name string) {
		switch name {
		case "ddns":
			ddnsMgr.Reload()
		case "reverseproxy":
			proxyMgr.Reload()
		case "cert":
			certMgr.Reload()
		}
	})

	if store.Get().Base.AutoStart {
		ddnsMgr.Reload()
		proxyMgr.Reload()
		certMgr.Start()
	} else {
		logger.Info("main", "自动启动已关闭（可在设置中开启）")
		certMgr.Start()
	}

	warnIfNotElevated()
	srv := webapi.New(store, ddnsMgr, certMgr, proxyMgr)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run() }()

	if *openWeb {
		scheme := "http"
		if store.Get().Base.EnableTLS {
			scheme = "https"
		}
		url := fmt.Sprintf("%s://127.0.0.1:%d/", scheme, store.Get().Base.AdminWebListenPort)
		go func() {
			time.Sleep(700 * time.Millisecond)
			if err := openBrowser(url); err != nil {
				logger.Warn("main", "自动打开浏览器失败(%v)，请手动访问 %s", err, url)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("main", "管理端退出: %v", err)
			os.Exit(1)
		}
	case sig := <-sigCh:
		logger.Info("main", "收到信号 %v，正在退出", sig)
		proxyMgr.Stop()
		ddnsMgr.Stop()
		certMgr.Stop()
		srv.Shutdown()
	}
	logger.Info("main", "已退出")
}

func setLogLevel(level string) {
	switch level {
	case "debug":
		logger.SetLevel(logger.LevelDebug)
	case "warn":
		logger.SetLevel(logger.LevelWarn)
	case "error":
		logger.SetLevel(logger.LevelError)
	default:
		logger.SetLevel(logger.LevelInfo)
	}
}
