package buildinfo

import (
	"runtime"
	"runtime/debug"
)

var (
	version string
	commit  string
)

type Info struct {
	Version   string
	Commit    string
	GoVersion string
}

func Current() Info {
	goInfo, ok := debug.ReadBuildInfo()
	if !ok {
		goInfo = nil
	}
	return resolve(version, commit, runtime.Version(), goInfo)
}

func resolve(injectedVersion, injectedCommit, goVersion string, goInfo *debug.BuildInfo) Info {
	info := Info{
		Version:   injectedVersion,
		Commit:    injectedCommit,
		GoVersion: goVersion,
	}
	if goInfo != nil {
		if info.Version == "" && goInfo.Main.Version != "" && goInfo.Main.Version != "(devel)" {
			info.Version = goInfo.Main.Version
		}
		if info.Commit == "" {
			info.Commit = buildSetting(goInfo.Settings, "vcs.revision")
			if info.Commit != "" && buildSetting(goInfo.Settings, "vcs.modified") == "true" {
				info.Commit += "-dirty"
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.GoVersion == "" {
		info.GoVersion = "unknown"
	}
	return info
}

func buildSetting(settings []debug.BuildSetting, key string) string {
	for _, setting := range settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}
