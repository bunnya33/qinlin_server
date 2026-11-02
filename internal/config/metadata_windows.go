package config

import "os"

func preserveMetadata(f *os.File, info os.FileInfo) error { return f.Chmod(info.Mode().Perm()) }
