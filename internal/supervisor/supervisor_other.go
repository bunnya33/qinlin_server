//go:build !linux

package supervisor

import (
	"context"
	"errors"
	"qinlin/internal/config"
)

func Run(context.Context, config.Config) error {
	return errors.New("automatic restart is available for managed Linux installations")
}
