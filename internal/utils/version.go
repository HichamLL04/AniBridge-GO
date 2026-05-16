package utils

import "runtime"

var Version = "2.1.3"

func GoVersion() string { return runtime.Version() }
