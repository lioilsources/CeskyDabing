//go:build !darwin

package main

func fsType(string) string { return "" }
