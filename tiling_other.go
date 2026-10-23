//go:build !linux

package main

// Tiling compositors are a Linux thing; elsewhere the window manager honours sizes.

func tilingName() string          { return "" }
func tilingFloat(w, h int)        {}
func tilingRestore()              {}
func tilingCheck() (bool, string) { return true, "" }
