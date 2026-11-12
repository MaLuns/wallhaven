package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGSettingsEnablesOnlyDisabledImageModes(t *testing.T) {
	for _, test := range []struct {
		name, desktop, imageKey, mode, draw, wantMode, wantDraw string
	}{
		{"gnome-none", "GNOME", "picture-uri", `'none'`, "", `'zoom'`, ""},
		{"cinnamon-none", "X-Cinnamon", "picture-uri", `'none'`, "", `'zoom'`, ""},
		{"mate-background-off", "MATE", "picture-filename", `'zoom'`, "false", `'zoom'`, "true"},
		{"gnome-spanned", "GNOME", "picture-uri", `'spanned'`, "", `'spanned'`, ""},
		{"cinnamon-centered", "X-Cinnamon", "picture-uri", `'centered'`, "", `'centered'`, ""},
		{"mate-enabled", "MATE", "picture-filename", `'centered'`, "true", `'centered'`, "true"},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{"picture-options": test.mode, "draw-background": test.draw}
			writes := map[string]int{}
			wallpaper := linuxBackend(t, test.desktop, func(_ context.Context, _ string, args ...string) ([]byte, error) {
				switch args[0] {
				case "list-keys":
					keys := test.imageKey + "\npicture-options\n"
					if test.draw != "" {
						keys += "draw-background\n"
					}
					return []byte(keys), nil
				case "writable":
					return []byte("true\n"), nil
				case "set":
					values[args[2]] = args[3]
					writes[args[2]]++
				case "get":
					return []byte(values[args[2]] + "\n"), nil
				}
				return nil, nil
			})
			if err := wallpaper.Set(context.Background(), wallpaperFile(t, "image.png")); err != nil {
				t.Fatal(err)
			}
			mode, err := variantString(values["picture-options"])
			wantMode, _ := variantString(test.wantMode)
			if err != nil || mode != wantMode || values["draw-background"] != test.wantDraw {
				t.Fatalf("wrong rendering state: %v (%v)", values, err)
			}
			if test.mode != `'none'` && writes["picture-options"] != 0 {
				t.Fatal("changed an already-enabled placement mode")
			}
			if test.draw != "false" && writes["draw-background"] != 0 {
				t.Fatal("changed an already-enabled or absent background switch")
			}
		})
	}
}

func TestGSettingsDisabledModeFailuresNeverSucceed(t *testing.T) {
	for _, key := range []string{"picture-options", "draw-background"} {
		for _, failure := range []string{"read-failed", "locked", "set-failed", "readback-mismatch"} {
			t.Run(key+"/"+failure, func(t *testing.T) {
				desktop, imageKey, disabled := "GNOME", "picture-uri", `'none'`
				if key == "draw-background" {
					desktop, imageKey, disabled = "MATE", "picture-filename", "false"
				}
				values := map[string]string{key: disabled}
				attempted := false
				wallpaper := linuxBackend(t, desktop, func(_ context.Context, _ string, args ...string) ([]byte, error) {
					switch args[0] {
					case "list-keys":
						return []byte(imageKey + "\n" + key), nil
					case "writable":
						if args[2] == key && failure == "locked" {
							return []byte("false"), nil
						}
						return []byte("true"), nil
					case "set":
						if args[2] == key {
							attempted = true
							if failure == "set-failed" {
								return nil, errors.New("write rejected")
							}
							if failure == "readback-mismatch" {
								return nil, nil
							}
						}
						values[args[2]] = args[3]
					case "get":
						if args[2] == key && failure == "read-failed" {
							return nil, errors.New("read rejected")
						}
						return []byte(values[args[2]]), nil
					}
					return nil, nil
				})
				if err := wallpaper.Set(context.Background(), wallpaperFile(t, "image.png")); err == nil {
					t.Fatal("reported success with image rendering still disabled")
				}
				if (failure == "locked" || failure == "read-failed") && attempted {
					t.Fatal("wrote a locked or unreadable rendering option")
				}
			})
		}
	}
}

func TestXfceEnablesDisabledModeAndPreservesOtherMonitors(t *testing.T) {
	path := wallpaperFile(t, "image.png")
	off := "/backdrop/screen0/monitorHDMI-1/workspace0/"
	tiled := "/backdrop/screen0/monitoreDP-1/workspace1/"
	defaults := "/backdrop/screen0/monitorDP-1/workspace0/"
	values := map[string]string{off + "image-style": "0", tiled + "image-style": "2"}
	writes := map[string]string{}
	listed := strings.Join([]string{off + "last-image", off + "image-style", tiled + "last-image", tiled + "image-style", defaults + "last-image"}, "\n")
	wallpaper := linuxBackend(t, "XFCE", func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[2] == "--list" {
			return []byte(listed), nil
		}
		if len(args) == 6 {
			values[args[3]], writes[args[3]] = args[5], args[5]
			return nil, nil
		}
		return []byte(values[args[3]] + "\n"), nil
	})
	if err := wallpaper.Set(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if values[off+"image-style"] != "5" || values[tiled+"image-style"] != "2" || values[defaults+"image-style"] != "" {
		t.Fatalf("wrong XFCE rendering modes: %v", values)
	}
	if _, changed := writes[tiled+"image-style"]; changed {
		t.Fatal("changed a visible tiled placement")
	}
	if _, created := writes[defaults+"image-style"]; created {
		t.Fatal("overrode the upstream default placement")
	}
	for _, prefix := range []string{off, tiled, defaults} {
		if values[prefix+"last-image"] != path {
			t.Fatalf("monitor did not receive the image: %s", prefix)
		}
	}
}

func TestXfceDisabledModeFailuresNeverSucceed(t *testing.T) {
	for _, failure := range []string{"read-failed", "set-failed", "readback-mismatch"} {
		t.Run(failure, func(t *testing.T) {
			prefix := "/backdrop/screen0/monitor0/workspace0/"
			values := map[string]string{prefix + "image-style": "0"}
			wallpaper := linuxBackend(t, "XFCE", func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if args[2] == "--list" {
					return []byte(prefix + "last-image\n" + prefix + "image-style"), nil
				}
				if strings.HasSuffix(args[3], "image-style") {
					if len(args) == 6 {
						if failure == "set-failed" {
							return nil, errors.New("mode locked")
						}
						if failure != "readback-mismatch" {
							values[args[3]] = args[5]
						}
					} else if failure == "read-failed" {
						return nil, errors.New("mode unreadable")
					}
				} else if len(args) == 6 {
					values[args[3]] = args[5]
				}
				return []byte(values[args[3]] + "\n"), nil
			})
			if err := wallpaper.Set(context.Background(), wallpaperFile(t, "image.png")); err == nil {
				t.Fatal("reported success while XFCE rendering remained disabled")
			}
		})
	}
}
