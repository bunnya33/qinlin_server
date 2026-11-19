package version

// Release builds override these values with -ldflags.
var Version = "0.4.2"
var Revision = "dev"

type Info struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

func Current() Info { return Info{Version, Revision} }
