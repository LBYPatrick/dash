package usb

import (
	"context"
	"errors"
)

func Serve(context.Context, func() string) error {
	return errors.New("USB mount service requires Linux")
}
