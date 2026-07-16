package main

import (
	"fmt"
	"reflect"
	"go.starlark.net/syntax"
)

func main() {
	t := reflect.TypeOf(syntax.FileOptions{})
	for i := 0; i < t.NumField(); i++ {
		fmt.Println(t.Field(i).Name)
	}
}
