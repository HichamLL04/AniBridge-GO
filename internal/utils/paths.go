package utils

import "path/filepath"

func InDataDir(dataDir, name string) string { return filepath.Join(dataDir, name) }
