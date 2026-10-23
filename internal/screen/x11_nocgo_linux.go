//go:build linux && !cgo

package screen

import (
	"errors"
	"image"
)

const x11Built = false

func x11Win(skip int, id uint64) (Win, error) { return Win{}, errors.New("built without X11 support") }

func x11Capture(w Win, full bool) (image.Image, image.Point, error) {
	return nil, image.Point{}, errors.New("built without X11 support")
}
