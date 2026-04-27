package version

import (
	"fmt"
	"runtime"
)

var (
	buildVersion   = "unknown"
	buildTimestamp = "unknown"
	buildStatus    = "unknown"
)

// BuildInfo describes version information about the binary build.
type BuildInfo struct {
	Version        string `json:"version"`
	BuildTimestamp string `json:"timestamp"`
	GolangVersion  string `json:"golang_version"`
	BuildStatus    string `json:"status"`
}

var (
	Info BuildInfo
)

// String produces a single-line version info
func (b BuildInfo) String() string {
	return fmt.Sprintf("%v-%v-%v",
		b.Version,
		b.BuildTimestamp,
		b.BuildStatus)
}

// LongForm returns a dump of the Info struct
func (b BuildInfo) LongForm() string {
	return fmt.Sprintf("%#v", b)
}

func (b BuildInfo) Map() map[string]string {
	return map[string]string{
		"build_tag":      b.Version,
		"build_time":     b.BuildTimestamp,
		"golang_version": b.GolangVersion,
		"build_status":   b.BuildStatus,
	}
}

func init() {
	Info = BuildInfo{
		Version:        buildVersion,
		BuildTimestamp: buildTimestamp,
		GolangVersion:  runtime.Version(),
		BuildStatus:    buildStatus,
	}
}
