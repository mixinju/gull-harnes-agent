package tool

type Tool interface {
	Name() string
	Description() string
	Version() string
	Exec() (error, string)
}
