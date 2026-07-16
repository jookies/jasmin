package main

import (
	"fmt"
	"reflect"
	"go.starlark.net/starlark"
)

func main() {
	// We can't easily list package members via reflect, but we can check if NewProgram exists via a hack or just look at common ones.
	// Actually, let's try to compile a small snippet that uses what we think exists.
}
