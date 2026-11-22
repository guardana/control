//go:build !linux && !darwin

package files

import "os"

func openJudged(*os.Root, string) (*os.File, error) { return nil, ErrNoExchange }

func lockFile(*os.File) error { return ErrNoExchange }
