package plugin

import "github.com/krau/mygotg/ext"

type Context struct {
	*ext.Context
	Args []string
	Cmd  string
}
