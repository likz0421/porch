'use strict';
'require view';
'require form';

return view.extend({
	render: function () {
		var m, s, o;

		m = new form.Map('porch', _('Porch 基础设置'),
			_('配置 Porch 服务的启动参数。修改后需重启服务生效。'));

		s = m.section(form.NamedSection, 'main', 'porch', _('服务设置'));

		o = s.option(form.Flag, 'enabled', _('启用服务'));
		o.default = '1';
		o.rmempty = false;

		o = s.option(form.Value, 'config_dir', _('配置目录'));
		o.default = '/etc/porch/porchconf';
		o.datatype = 'string';
		o.rmempty = false;
		o.description = _('存放 base.json / ddns.json / cert.json / reverseproxy.json 与证书文件');

		o = s.option(form.Value, 'port', _('管理端口'));
		o.default = '';
		o.datatype = 'port';
		o.optional = true;
		o.description = _('留空则用程序内设置的端口（默认 16601）。这里填了会作为启动参数固定住，' +
			'优先级高于程序内设置；改完需重启服务。');

		// 账号密码、外网访问开关、日志级别这些是程序运行时配置，
		// 存在 base.json 里、由程序自己的网页写入。这里只说明去哪儿改，
		// 不做成「看着能改其实没接进启动参数」的假开关。
		o = s.option(form.DummyValue, '_webui', _('更多设置'));
		o.rawhtml = false;
		o.cfgvalue = function () {
			return _('管理员账号密码、是否允许外网访问、日志级别，请在「状态」页点「打开管理页面」后设置。');
		};

		return m.render();
	}
});
