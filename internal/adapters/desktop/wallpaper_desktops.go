package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Linux support is deliberately limited to the desktop-owned settings APIs
// below. A session bus and the corresponding tool/schema must be available.
// No fallback claims success after merely submitting an asynchronous request.
func (w *Wallpaper) setLinuxWallpaper(ctx context.Context, path string) error {
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	switch detectedDesktop(w.getenv) {
	case "gnome":
		return w.setGSettings(ctx, "org.gnome.desktop.background", "picture-uri", uri)
	case "cinnamon":
		return w.setGSettings(ctx, "org.cinnamon.desktop.background", "picture-uri", uri)
	case "mate":
		return w.setGSettings(ctx, "org.mate.background", "picture-filename", path)
	case "xfce":
		return w.setXfce(ctx, path)
	case "plasma":
		return w.setPlasma(ctx, uri)
	default:
		return actionError{"当前 Linux 桌面暂不支持设置壁纸（支持 GNOME、Cinnamon、MATE、XFCE、KDE Plasma）", ErrUnsupported}
	}
}

func detectedDesktop(getenv func(string) string) string {
	for _, variable := range []string{"XDG_CURRENT_DESKTOP", "DESKTOP_SESSION", "XDG_SESSION_DESKTOP"} {
		value := strings.TrimSpace(getenv(variable))
		for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r == ':' || r == ';' }) {
			token = filepath.Base(strings.TrimSpace(token))
			switch token {
			case "gnome", "gnome-classic", "gnome-xorg", "ubuntu", "ubuntu-wayland":
				return "gnome"
			case "cinnamon", "x-cinnamon":
				return "cinnamon"
			case "mate":
				return "mate"
			case "xfce", "xfce4", "x-xfce":
				return "xfce"
			case "kde", "plasma", "plasma5", "plasma6", "plasmawayland", "plasma-x11", "plasma-wayland":
				return "plasma"
			}
		}
		if variable == "XDG_CURRENT_DESKTOP" && value != "" {
			return "" // An explicitly unknown current desktop wins over stale session hints.
		}
	}
	return ""
}

func (w *Wallpaper) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	program, err := w.lookPath(name)
	if err != nil {
		return nil, actionError{"设置壁纸需要系统命令 " + name, ErrUnsupported}
	}
	output, err := w.run(ctx, program, args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, errors.New(name + " 未能应用壁纸，请检查桌面会话和系统权限")
	}
	return output, nil
}

// The keys are defined by the desktop projects' schemas. Read-back verifies the
// persisted value as command exit status alone can hide a dconf commit failure.
// https://github.com/GNOME/gsettings-desktop-schemas/blob/master/schemas/org.gnome.desktop.background.gschema.xml.in
// https://github.com/linuxmint/cinnamon-desktop/blob/master/schemas/org.cinnamon.desktop.background.gschema.xml.in
// https://github.com/mate-desktop/mate-desktop/blob/master/schemas/org.mate.background.gschema.xml.in
func (w *Wallpaper) setGSettings(ctx context.Context, schema, key, value string) error {
	available, err := w.command(ctx, "gsettings", "list-keys", schema)
	if err != nil {
		return err
	}
	keys := map[string]bool{}
	for _, field := range strings.Fields(string(available)) {
		keys[field] = true
	}
	if !keys[key] {
		return actionError{"当前桌面没有可用的壁纸设置项", ErrUnsupported}
	}
	targets := []string{key}
	if key == "picture-uri" && keys["picture-uri-dark"] {
		targets = append(targets, "picture-uri-dark")
	}
	encoded, _ := json.Marshal(value) // JSON string escapes are valid GVariant string escapes.
	for _, target := range targets {
		if err := w.writeGSettingsValue(ctx, schema, target, string(encoded)); err != nil {
			return err
		}
	}
	// A new image URI alone does not display a picture while rendering is
	// explicitly disabled. Preserve every active placement mode.
	if keys["picture-options"] {
		stored, err := w.command(ctx, "gsettings", "get", schema, "picture-options")
		if err != nil {
			return err
		}
		mode, err := variantString(string(stored))
		if err != nil {
			return errors.New("无法确认桌面壁纸显示模式")
		}
		if mode == "none" {
			if err := w.writeGSettingsValue(ctx, schema, "picture-options", `"zoom"`); err != nil {
				return err
			}
		}
	}
	// MATE has a separate draw-background boolean instead of a "none" enum.
	if keys["draw-background"] {
		stored, err := w.command(ctx, "gsettings", "get", schema, "draw-background")
		if err != nil {
			return err
		}
		switch strings.TrimSpace(string(stored)) {
		case "false":
			if err := w.writeGSettingsValue(ctx, schema, "draw-background", "true"); err != nil {
				return err
			}
		case "true":
		default:
			return errors.New("无法确认桌面背景显示开关")
		}
	}
	return nil
}

// literal is a string or boolean GVariant value. Writes are acknowledged by
// both the settings service and a read-back, including rendering-mode changes.
func (w *Wallpaper) writeGSettingsValue(ctx context.Context, schema, key, literal string) error {
	writable, err := w.command(ctx, "gsettings", "writable", schema, key)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(writable)) != "true" {
		return errors.New("壁纸设置已被系统策略锁定")
	}
	if _, err := w.command(ctx, "gsettings", "set", schema, key, literal); err != nil {
		return err
	}
	stored, err := w.command(ctx, "gsettings", "get", schema, key)
	if err != nil {
		return err
	}
	if literal == "true" || literal == "false" {
		if strings.TrimSpace(string(stored)) != literal {
			return errors.New("桌面未保存新的壁纸显示开关，请检查系统权限")
		}
	} else {
		expected, expectedErr := variantString(literal)
		actual, err := variantString(string(stored))
		if expectedErr != nil || err != nil || actual != expected {
			return errors.New("桌面未保存新的壁纸设置，请检查系统权限")
		}
	}
	return nil
}

func (w *Wallpaper) enableXfceImageMode(ctx context.Context, property string) error {
	stored, err := w.command(ctx, "xfconf-query", "--channel", "xfce4-desktop", "--property", property)
	if err != nil {
		return err
	}
	mode, err := strconv.Atoi(strings.TrimSpace(string(stored)))
	if err != nil {
		return errors.New("无法确认 XFCE 壁纸显示模式")
	}
	if mode != 0 {
		return nil
	}
	// XfceBackdropImageStyle defines NONE=0 and ZOOMED=5. An absent property
	// already defaults to ZOOMED in xfdesktop-backdrop-manager.c.
	// https://github.com/xfce-mirror/xfdesktop/blob/master/common/xfdesktop-common.h
	if _, err := w.command(ctx, "xfconf-query", "--channel", "xfce4-desktop", "--property", property, "--set", "5"); err != nil {
		return err
	}
	stored, err = w.command(ctx, "xfconf-query", "--channel", "xfce4-desktop", "--property", property)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(stored)) != "5" {
		return errors.New("XFCE 未启用新的壁纸显示模式")
	}
	return nil
}

// GVariant prints either quote style and supports Unicode/C-style escapes.
// Convert canonical string output into a Go double-quoted literal without
// treating it as a shell expression or accepting another GVariant type.
// https://docs.gtk.org/glib/gvariant-text-format.html#strings
func variantString(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != '\'' && value[0] != '"' || value[len(value)-1] != value[0] {
		return "", errors.New("壁纸设置响应不是字符串")
	}
	var text strings.Builder
	text.WriteByte('"')
	for i := 1; i < len(value)-1; i++ {
		character := value[i]
		if character == '\\' {
			i++
			if i >= len(value)-1 {
				return "", errors.New("壁纸设置响应转义无效")
			}
			if value[i] == '\'' {
				text.WriteByte('\'')
			} else {
				text.WriteByte('\\')
				text.WriteByte(value[i])
			}
		} else if character == value[0] {
			return "", errors.New("壁纸设置响应引号无效")
		} else if character == '"' {
			text.WriteString(`\"`)
		} else {
			text.WriteByte(character)
		}
	}
	text.WriteByte('"')
	return strconv.Unquote(text.String())
}

var xfceImageProperty = regexp.MustCompile(`^/backdrop/screen[0-9]+/monitor[^/]+/(workspace[0-9]+/)?last-image$`)

// XFCE reads last-image for each configured monitor/workspace. Only existing
// backdrop keys are touched; an empty/unrecognised layout is unsupported.
// https://docs.xfce.org/xfce/xfconf/xfconf-query
// https://github.com/xfce-mirror/xfdesktop/blob/master/src/xfdesktop-backdrop-manager.c
func (w *Wallpaper) setXfce(ctx context.Context, path string) error {
	listed, err := w.command(ctx, "xfconf-query", "--channel", "xfce4-desktop", "--list")
	if err != nil {
		return err
	}
	properties := map[string]bool{}
	for _, line := range strings.Split(string(listed), "\n") {
		properties[strings.TrimSpace(line)] = true
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(listed), "\n") {
		property := strings.TrimSpace(line)
		if !xfceImageProperty.MatchString(property) || seen[property] {
			continue
		}
		seen[property] = true
		if _, err := w.command(ctx, "xfconf-query", "--channel", "xfce4-desktop", "--property", property, "--set", path); err != nil {
			return err
		}
		stored, err := w.command(ctx, "xfconf-query", "--channel", "xfce4-desktop", "--property", property)
		if err != nil {
			return err
		}
		if strings.TrimSuffix(string(stored), "\n") != path {
			return errors.New("XFCE 未保存新的壁纸设置")
		}
		modeProperty := strings.TrimSuffix(property, "last-image") + "image-style"
		if properties[modeProperty] {
			if err := w.enableXfceImageMode(ctx, modeProperty); err != nil {
				return err
			}
		}
	}
	if len(seen) == 0 {
		return actionError{"未找到 XFCE 当前屏幕的壁纸配置", ErrUnsupported}
	}
	return nil
}

var plasmaSuccess = regexp.MustCompile(`^\('MYGO_WALLPAPER_OK:[1-9][0-9]*',\)$`)

// evaluateScript is synchronous. A JSON string embeds the URI safely in the
// Plasma scripting language, and a verified marker distinguishes script errors
// inside a successful D-Bus reply from an applied setting.
// https://develop.kde.org/docs/plasma/scripting/api/
func (w *Wallpaper) setPlasma(ctx context.Context, uri string) error {
	encoded, _ := json.Marshal(uri)
	script := `var image = ` + string(encoded) + `;
var ds = desktops();
if (ds.length < 1) throw new Error("No desktops");
for (var i = 0; i < ds.length; i++) {
    var d = ds[i];
    d.wallpaperPlugin = "org.kde.image";
    d.currentConfigGroup = ["Wallpaper", "org.kde.image", "General"];
    d.writeConfig("Image", image);
    if (String(d.readConfig("Image")) !== image) throw new Error("Wallpaper not applied");
}
print("MYGO_WALLPAPER_OK:" + ds.length);`
	argument, _ := json.Marshal(script) // One GVariant string parameter, not source for a shell.
	output, err := w.command(ctx, "gdbus", "call", "--session", "--dest", "org.kde.plasmashell", "--object-path", "/PlasmaShell", "--method", "org.kde.PlasmaShell.evaluateScript", string(argument))
	if err != nil {
		return err
	}
	if !plasmaSuccess.MatchString(strings.TrimSpace(string(output))) {
		return errors.New("KDE Plasma 未确认壁纸已应用")
	}
	return nil
}
