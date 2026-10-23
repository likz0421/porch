'use strict';
'require view';
'require rpc';
'require ui';

var callStatus = rpc.declare({
	object: 'porch',
	method: 'status',
	expect: { '': {} }
});

var callAction = rpc.declare({
	object: 'porch',
	method: '%s',
	expect: { '': {} }
});

function row(label, value) {
	return E('div', { 'class': 'tr' }, [
		E('div', { 'class': 'td left', 'style': 'width:220px' }, label),
		E('div', { 'class': 'td left' }, value)
	]);
}

function parsePing(str) {
	try {
		return JSON.parse(str || '{}');
	} catch (e) {
		return {};
	}
}

function handleAction(action) {
	return callAction(action).then(function () {
		setTimeout(function () { window.location.reload(); }, 1200);
	}).catch(function (e) {
		ui.addNotification(null, E('p', {}, _('操作失败') + ': ' + e.message));
	});
}

return view.extend({
	load: function () {
		return callStatus();
	},

	render: function (info) {
		info = info || {};
		var ping = parsePing(info.ping);
		var running = info.running === true || info.running === 'true' || info.running === 1;
		var port = info.port || ping.port || '';

		return E('div', { 'class': 'cbi-map' }, [
			E('h2', { 'name': 'content' }, _('Porch 服务状态')),
			E('div', { 'class': 'cbi-section' }, [
				E('div', { 'class': 'table' }, [
					row(_('运行状态'), running
						? E('span', { 'style': 'color:#2ba36b;font-weight:bold' }, _('运行中'))
						: E('span', { 'style': 'color:#d9534f;font-weight:bold' }, _('已停止'))),
					row(_('程序版本'), ping.version || '-'),
					row(_('管理端口'), String(port || 16601)),
					row(_('配置目录'), info.confDir || '-'),
					row(_('动态域名任务'), String(ping.ddns || 0)),
					row(_('证书任务'), String(ping.cert || 0)),
					row(_('反代规则'), String(ping.proxy || 0)),
					row(_('运行时长'), ping.uptime || '-')
				])
			]),
			E('div', { 'class': 'cbi-section' }, [
				E('div', { 'style': 'display:flex;gap:10px;flex-wrap:wrap' }, [
					E('button', {
						'class': 'btn cbi-button cbi-button-apply',
						'click': function () { handleAction('start'); }
					}, _('启动')),
					E('button', {
						'class': 'btn cbi-button cbi-button-negative',
						'click': function () { handleAction('stop'); }
					}, _('停止')),
					E('button', {
						'class': 'btn cbi-button cbi-button-action',
						'click': function () { handleAction('restart'); }
					}, _('重启')),
					E('button', {
						'class': 'btn cbi-button cbi-button-positive',
						'click': function () {
							window.open('http://' + window.location.hostname + ':' + (port || 16601) + '/', '_blank');
						}
					}, _('打开 Web 管理界面'))
				])
			]),
			E('div', { 'class': 'cbi-section' }, [
				E('div', { 'class': 'cbi-section-descr' },
					_('默认账号 666 / 密码 666。Web 界面包含动态域名、SSL 证书、反向代理与运行日志。'))
			])
		]);
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
