package main

import "github.com/krau/btts/cmd"

//go:generate swag init -g api/api.go --output api/docs

func main() {
	cmd.Execute()
}
