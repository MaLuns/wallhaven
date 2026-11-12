package desktop

import (
	"context"
	"errors"
	"os"

	"github.com/MaLuns/wallhaven-electron/internal/ports"
	"github.com/egoist/mygo"
)

// MyGO marshals these calls to the platform UI thread. No login item is
// registered merely by loading preferences or launching the application.
type LoginItems struct{}

var _ ports.LoginItems = LoginItems{}

func (LoginItems) Enabled(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return mygo.App.OpenAtLogin(), nil
}

func (LoginItems) SetEnabled(ctx context.Context, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return mygo.App.SetOpenAtLogin(enabled)
}

type SettingsDirectories struct{}

var _ ports.SettingsDirectories = SettingsDirectories{}

func (SettingsDirectories) Validate(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return errors.New("下载目录不存在或不是文件夹")
	}
	file, err := os.CreateTemp(path, ".wallhaven-write-*")
	if err != nil {
		return errors.New("下载目录不可写，请选择其他文件夹")
	}
	name := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(name)
	if closeErr != nil || removeErr != nil {
		return errors.New("无法在下载目录中完成文件操作")
	}
	return ctx.Err()
}

func (SettingsDirectories) Open(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The default subdirectory may not exist until the first download.
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return mygo.Shell.OpenPath(path)
}
