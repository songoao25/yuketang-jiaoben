package browser

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenURLRejectsEmpty(t *testing.T) {
	if err := OpenURL("   "); err == nil {
		t.Fatal("空网址应返回错误")
	}
}

func TestRevealRejectsEmpty(t *testing.T) {
	if err := Reveal(""); err == nil {
		t.Fatal("空路径应返回错误")
	}
}

func TestOpenCommandPerPlatform(t *testing.T) {
	cmd := openCommand("https://yuketang.cn/")
	if cmd == nil {
		t.Skipf("当前平台 %s 无浏览器命令", runtime.GOOS)
	}
	switch runtime.GOOS {
	case "darwin":
		assertCommand(t, cmd, "open", []string{"https://yuketang.cn/"})
	case "windows":
		assertCommand(t, cmd, "rundll32", []string{"url.dll,FileProtocolHandler", "https://yuketang.cn/"})
	case "linux":
		assertCommand(t, cmd, "xdg-open", []string{"https://yuketang.cn/"})
	default:
		t.Skipf("未覆盖的平台 %s", runtime.GOOS)
	}
}

func TestRevealCommandPerPlatform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cmd := revealCommand(path)
	if cmd == nil {
		t.Skipf("当前平台 %s 无定位命令", runtime.GOOS)
	}
	switch runtime.GOOS {
	case "darwin":
		assertCommand(t, cmd, "open", []string{"-R", path})
	case "windows":
		assertCommand(t, cmd, "explorer", []string{"/select," + path})
	case "linux":
		assertCommand(t, cmd, "xdg-open", []string{filepath.Dir(path)})
	default:
		t.Skipf("未覆盖的平台 %s", runtime.GOOS)
	}
}

// assertCommand 校验可执行文件名与参数。
func assertCommand(t *testing.T, cmd *exec.Cmd, wantBin string, wantArgs []string) {
	t.Helper()
	args := cmd.Args
	if len(args) == 0 {
		t.Fatal("命令参数为空")
	}
	gotBin := filepath.Base(args[0])
	if gotBin != wantBin {
		t.Fatalf("可执行文件 = %q, want %q", gotBin, wantBin)
	}
	gotArgs := args[1:]
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("参数数量 = %d, want %d (%v)", len(gotArgs), len(wantArgs), gotArgs)
	}
	for i, want := range wantArgs {
		if gotArgs[i] != want {
			t.Fatalf("参数[%d] = %q, want %q", i, gotArgs[i], want)
		}
	}
}
