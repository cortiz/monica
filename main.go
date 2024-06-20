package main

import (
	"jmpeax.com/sec/monica/pkg/cmd"
)

func main() {
	err := cmd.Execute()
	if err != nil {
		panic(err)
	}
}
