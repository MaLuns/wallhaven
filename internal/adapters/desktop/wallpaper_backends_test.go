package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func linuxBackend(t *testing.T, desktop string, runner Runner) *Wallpaper {
	t.Helper()
	return NewWithOptions(Options{Platform: "linux", Getenv: func(name string) string {
		if name == "XDG_CURRENT_DESKTOP" {
			return desktop
		}
		return ""
	}, LookPath: func(name string) (string, error) { return "/mock/" + name, nil }, Runner: runner})
}

func TestWallpaperDesktopDetection(t *testing.T) {
	for _, test := range []struct{ variable, value, want string }{
		{"XDG_CURRENT_DESKTOP", "ubuntu:GNOME", "gnome"},
		{"XDG_CURRENT_DESKTOP", "X-Cinnamon", "cinnamon"},
		{"XDG_CURRENT_DESKTOP", "MATE", "mate"},
		{"DESKTOP_SESSION", "/usr/share/xsessions/xfce", "xfce"},
		{"XDG_CURRENT_DESKTOP", "KDE", "plasma"},
		{"DESKTOP_SESSION", "plasmawayland", "plasma"},
		{"XDG_SESSION_DESKTOP", "plasma6", "plasma"},
		{"XDG_CURRENT_DESKTOP", "sway", ""},
		{"XDG_CURRENT_DESKTOP", "not-gnome", ""},
	} {
		got := detectedDesktop(func(name string) string {
			if name == test.variable {
				return test.value
			}
			return ""
		})
		if got != test.want {
			t.Errorf("%s=%q detected %q, want %q", test.variable, test.value, got, test.want)
		}
	}
	if got := detectedDesktop(func(name string) string {
		if name == "XDG_CURRENT_DESKTOP" {
			return "sway"
		}
		return "GNOME"
	}); got != "" {
		t.Fatalf("stale session hint overrode current unsupported desktop: %s", got)
	}
}

func TestGSettingsWallpapersUseSchemaEncodingAndReadBack(t *testing.T) {
	for _, test := range []struct{ desktop, schema, key string }{
		{"GNOME", "org.gnome.desktop.background", "picture-uri"},
		{"X-Cinnamon", "org.cinnamon.desktop.background", "picture-uri"},
		{"MATE", "org.mate.background", "picture-filename"},
	} {
		t.Run(test.desktop, func(t *testing.T) {
			path := wallpaperFile(t, "图片 'quoted' $value & space.png")
			want := path
			if test.key == "picture-uri" {
				want = (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
			}
			values := map[string]string{}
			wallpaper := linuxBackend(t, test.desktop, func(_ context.Context, program string, args ...string) ([]byte, error) {
				if program != "/mock/gsettings" || len(args) < 2 || args[1] != test.schema {
					t.Fatalf("wrong schema or tool: %s %q", program, args)
				}
				switch args[0] {
				case "list-keys":
					return []byte(test.key + "\npicture-uri-dark\n"), nil
				case "writable":
					return []byte("true\n"), nil
				case "set":
					if len(args) != 4 {
						t.Fatalf("path split into arguments: %q", args)
					}
					decoded, err := variantString(args[3])
					if err != nil || decoded != want {
						t.Fatalf("unsafe/wrong GVariant value: %q %v", args[3], err)
					}
					values[args[2]] = args[3]
					return nil, nil
				case "get":
					return []byte(values[args[2]] + "\n"), nil
				default:
					t.Fatalf("unexpected command: %q", args)
					return nil, nil
				}
			})
			if err := wallpaper.Set(context.Background(), path); err != nil {
				t.Fatal(err)
			}
			if values[test.key] == "" || test.key == "picture-uri" && values["picture-uri-dark"] == "" || test.key != "picture-uri" && values["picture-uri-dark"] != "" {
				t.Fatalf("wrong light/dark background keys: %v", values)
			}
		})
	}
}

func TestGSettingsFailureDoesNotReportSuccess(t *testing.T) {
	path := wallpaperFile(t, "wallpaper.png")
	for _, mode := range []string{"locked", "missing-key", "set-failed", "readback-mismatch", "legacy-no-dark"} {
		t.Run(mode, func(t *testing.T) {
			var saved string
			sets := 0
			wallpaper := linuxBackend(t, "GNOME", func(_ context.Context, _ string, args ...string) ([]byte, error) {
				switch args[0] {
				case "list-keys":
					if mode == "missing-key" {
						return []byte("picture-options\n"), nil
					}
					return []byte("picture-uri\n"), nil
				case "writable":
					if mode == "locked" {
						return []byte("false\n"), nil
					}
					return []byte("true\n"), nil
				case "set":
					sets++
					saved = args[3]
					if mode == "set-failed" {
						return []byte("secret path"), errors.New("commit failed")
					}
				case "get":
					if mode == "readback-mismatch" {
						return []byte("'old value'\n"), nil
					}
					return []byte(saved), nil
				}
				return nil, nil
			})
			err := wallpaper.Set(context.Background(), path)
			if mode == "legacy-no-dark" {
				if err != nil || sets != 1 {
					t.Fatalf("legacy GNOME failed: %v, sets=%d", err, sets)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("failed backend falsely succeeded or leaked diagnostics: %v", err)
			}
			if (mode == "locked" || mode == "missing-key") && sets != 0 {
				t.Fatal("modified locked/missing key")
			}
		})
	}
}

func TestVariantStringUnderstandsCanonicalQuotesAndUnicode(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{`'/home/it\'s \\a.png'`, "/home/it's \\a.png"},
		{`'a"b'`, `a"b`},
		{`"line\n\u56fe.png"`, "line\n图.png"},
	} {
		got, err := variantString(test.input)
		if err != nil || got != test.want {
			t.Errorf("%s decoded to %q, want %q: %v", test.input, got, test.want, err)
		}
	}
	for _, input := range []string{"true", "['image']", `'a'b'`, `"a"b"`, `'unterminated`} {
		if _, err := variantString(input); err == nil {
			t.Errorf("accepted malformed string: %s", input)
		}
	}
}

func TestXfceUpdatesExistingMonitorWorkspacesAndChecksReadback(t *testing.T) {
	path := wallpaperFile(t, "wallpaper.png")
	properties := []string{"/backdrop/screen0/monitorHDMI-1/workspace0/last-image", "/backdrop/screen0/monitoreDP-1/workspace1/last-image"}
	stored := map[string]string{}
	wallpaper := linuxBackend(t, "XFCE", func(_ context.Context, program string, args ...string) ([]byte, error) {
		if program != "/mock/xfconf-query" || !reflect.DeepEqual(args[:2], []string{"--channel", "xfce4-desktop"}) {
			t.Fatalf("wrong xfconf contract: %s %q", program, args)
		}
		if args[2] == "--list" {
			return []byte(strings.Join(append(properties, "/other/last-image", properties[0]), "\n")), nil
		}
		if len(args) == 6 {
			stored[args[3]] = args[5]
			return nil, nil
		}
		return []byte(stored[args[3]] + "\n"), nil
	})
	if err := wallpaper.Set(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 || stored[properties[0]] != path || stored[properties[1]] != path {
		t.Fatalf("wrong monitor/workspace updates: %v", stored)
	}
}

func TestXfceEmptyLayoutAndPartialFailureAreErrors(t *testing.T) {
	path := wallpaperFile(t, "wallpaper.png")
	for _, mode := range []string{"empty", "set-failed", "readback-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			wallpaper := linuxBackend(t, "XFCE", func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if args[2] == "--list" {
					if mode == "empty" {
						return []byte("/unrelated/last-image"), nil
					}
					return []byte("/backdrop/screen0/monitor0/workspace0/last-image"), nil
				}
				if len(args) == 6 && mode == "set-failed" {
					return nil, errors.New("write failed")
				}
				return []byte("old image\n"), nil
			})
			if err := wallpaper.Set(context.Background(), path); err == nil {
				t.Fatal("XFCE backend falsely succeeded")
			}
		})
	}
}

func TestPlasmaUsesSynchronousDBusAndSafelyEncodedScript(t *testing.T) {
	name := "图片 'quoted' & file.png"
	if runtime.GOOS != "windows" {
		name = "图片 'quoted' \\ file\n.png"
	}
	path := wallpaperFile(t, name)
	want := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	wallpaper := linuxBackend(t, "KDE", func(_ context.Context, program string, args ...string) ([]byte, error) {
		if program != "/mock/gdbus" || len(args) != 9 || !reflect.DeepEqual(args[:8], []string{"call", "--session", "--dest", "org.kde.plasmashell", "--object-path", "/PlasmaShell", "--method", "org.kde.PlasmaShell.evaluateScript"}) {
			t.Fatalf("wrong DBus contract: %s %q", program, args)
		}
		var script string
		if err := json.Unmarshal([]byte(args[len(args)-1]), &script); err != nil {
			t.Fatalf("script was not one quoted GVariant string: %v", err)
		}
		literal, _, ok := strings.Cut(strings.TrimPrefix(script, "var image = "), ";\n")
		var actual string
		if !ok || json.Unmarshal([]byte(literal), &actual) != nil || actual != want || !strings.Contains(script, "d.readConfig") || !strings.Contains(script, "MYGO_WALLPAPER_OK") {
			t.Fatalf("URI was not safely encoded and verified: %s", script)
		}
		return []byte("('MYGO_WALLPAPER_OK:2',)\n"), nil
	})
	if err := wallpaper.Set(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	for _, response := range []string{"()", "('Error: blocked',)", "('MYGO_WALLPAPER_OK:0',)"} {
		wallpaper := linuxBackend(t, "KDE", func(context.Context, string, ...string) ([]byte, error) { return []byte(response), nil })
		if err := wallpaper.Set(context.Background(), path); err == nil {
			t.Fatalf("accepted unconfirmed D-Bus reply %q", response)
		}
	}
}

func TestLinuxUnsupportedDependenciesAndTimeout(t *testing.T) {
	path := wallpaperFile(t, "wallpaper.png")
	called := false
	wallpaper := linuxBackend(t, "sway", func(context.Context, string, ...string) ([]byte, error) { called = true; return nil, nil })
	if err := wallpaper.Set(context.Background(), path); !errors.Is(err, ErrUnsupported) || called {
		t.Fatalf("unknown desktop error=%v, command=%t", err, called)
	}
	wallpaper = linuxBackend(t, "GNOME", func(context.Context, string, ...string) ([]byte, error) { called = true; return nil, nil })
	wallpaper.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if err := wallpaper.Set(context.Background(), path); !errors.Is(err, ErrUnsupported) || called {
		t.Fatalf("missing tool error=%v, command=%t", err, called)
	}
	wallpaper = linuxBackend(t, "GNOME", func(ctx context.Context, _ string, _ ...string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() })
	wallpaper.timeout = 15 * time.Millisecond
	if err := wallpaper.Set(context.Background(), path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Linux timeout was not returned: %v", err)
	}
}

func TestWindowsWallpaperUsesNativeHookAndReportsFailure(t *testing.T) {
	path := wallpaperFile(t, "壁纸 'quoted'.png")
	called := false
	wallpaper := NewWithOptions(Options{Platform: "windows", Runner: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("Windows used a shell command")
		return nil, nil
	}, WindowsSet: func(ctx context.Context, actual string) error {
		called = true
		if actual != path {
			t.Fatalf("native Windows path changed: %q", actual)
		}
		if _, exists := ctx.Deadline(); !exists {
			t.Fatal("native Windows call has no context deadline")
		}
		return nil
	}})
	if err := wallpaper.Set(context.Background(), path); err != nil || !called {
		t.Fatalf("Windows native hook error=%v, called=%t", err, called)
	}
	wallpaper.windows = func(context.Context, string) error { return errors.New("Windows rejected image") }
	if err := wallpaper.Set(context.Background(), path); err == nil {
		t.Fatal("native Windows failure falsely succeeded")
	}
}
