package sandbox

import "gull-herness-agent/config"

// OptionsFromConfig 把配置文件里的 sandbox 段转成 Options。
// 缺省字段保持默认安全：开启、restricted、不放行网络。
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
	if c.BashMode != "" {
		opt.BashMode = c.BashMode
	}
	if c.AllowNetwork != nil {
		opt.AllowNetwork = *c.AllowNetwork
	}
	if c.ProcessIsolation != "" {
		opt.ProcessIsolation = c.ProcessIsolation
	}
	if c.ExtraDeniedCommands != nil {
		opt.ExtraDeniedCommands = append([]string(nil), c.ExtraDeniedCommands...)
	}
	return opt, nil
}
