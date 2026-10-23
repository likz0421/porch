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
		o.description = _('留空则使用程序配置中的端口（默认 16601）');

		o = s.option(form.Flag, 'allow_internet', _('允许外网访问'));
		o.default = '0';
		o.description = _('关闭时仅允许局域网访问管理页面');

		return m.render();
	}
});
