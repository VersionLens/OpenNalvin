package buildinfo

const (
	Name   = "nalvin"
	Module = "github.com/versionlens/OpenNalvin"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

type Info struct {
	Name    string `json:"name"`
	Module  string `json:"module"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func Current() Info {
	return Info{
		Name:    Name,
		Module:  Module,
		Version: Version,
		Commit:  Commit,
		Date:    Date,
	}
}
