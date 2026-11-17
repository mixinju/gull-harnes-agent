package sandbox

import "gull-herness-agent/config"

// OptionsFromConfig 把配置文件里的 sandbox 段转成 Options。
// 缺省保持默认：开启，并且不把网络放进沙箱。
func OptionsFromConfig(c *config.SandboxConfig) (Options, error) {
	opt := DefaultOptions()
	if c == nil {
		return opt, nil
	}
	if c.Enabled != nil {
		opt.Enabled = *c.Enabled
	}
	if c.Workspace != "" {
		opt.Workspace = c.Workspace
	}
	if c.AllowedPaths != nil {
		opt.AllowedPaths = append([]string(nil), c.AllowedPaths...)
	}
	if c.ReadOnlyPaths != nil {
		opt.ReadOnlyPaths = append([]string(nil), c.ReadOnlyPaths...)
	}
	if c.AllowNetwork != nil {
		opt.AllowNetwork = *c.AllowNetwork
	}
	return opt, nil
}
