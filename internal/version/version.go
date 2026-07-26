// Package version 提供构建期可注入的版本信息。
package version

// Version 是服务版本号，可用 -ldflags "-X ...Version=x.y.z" 覆盖。
var Version = "0.1.0"
