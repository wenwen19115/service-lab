//go:build !windows

package main

import "fmt"

func showStartupError(message string) {
	fmt.Println(message)
}
