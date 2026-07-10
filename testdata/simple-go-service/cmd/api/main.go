package main

import "example.com/simple/internal/auth"

func main() {
	manager := auth.SessionManager{}
	manager.CreateSession("user-1")
}
