package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/process"
	"github.com/auto-deployer/auto-deployer/internal/registry"
)

const defaultPidDir = ".deployd/run"

// Stop stops the deployd daemon.
func Stop(configPath string) error {
	pidFile := filepath.Join(homeDir(configPath), defaultPidDir, "deployd.pid")
	mgr := process.NewManager(pidFile)

	if mgr.Status() != "running" {
		fmt.Println("deployd is not running")
		return nil
	}

	return mgr.Stop()
}

// Status shows the status of deployd and all configured services.
func Status(configPath string) error {
	pidFile := filepath.Join(homeDir(configPath), defaultPidDir, "deployd.pid")
	mgr := process.NewManager(pidFile)

	fmt.Printf("deployd: %s\n", mgr.Status())

	cfgPath := configPath
	if cfgPath == "" {
		cfgPath = filepath.Join(homeDir(configPath), "config.yaml")
	}
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return nil
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	// 服务状态走 GetServiceStatusRich（starting/start_failed 持久态 + 插件实时探测），
	// 不再直读 pid 文件——static 这类无进程模型会恒报 stopped（C2）。
	for i := range cfg.Services {
		svc := &cfg.Services[i]
		st := "unknown"
		if d, err := registry.Get(svc.Type); err == nil {
			if s, err := deploy.GetServiceStatusRich(context.Background(), svc, d); err == nil {
				st = s
			}
		}
		fmt.Printf("  %-30s %s\n", svc.Name, st)
	}

	return nil
}

// Logs prints the contents of a log file.
// If tail > 0, shows only the last N lines.
// If follow is true, tails the file in real-time (like tail -f).
func Logs(serviceName, configPath, logFile string, tail int, follow bool) error {
	var lf string
	if logFile != "" {
		lf = logFile
	} else {
		logDir := filepath.Join(homeDir(configPath), ".deployd")
		if serviceName != "" {
			lf = filepath.Join(logDir, "services", serviceName+".log")
		} else {
			lf = filepath.Join(logDir, daemonLogName)
		}
	}

	// Check if file exists
	if _, err := os.Stat(lf); os.IsNotExist(err) {
		fmt.Println("no logs found")
		return nil
	}

	if follow {
		return tailFollow(lf, tail)
	}

	data, err := os.ReadFile(lf)
	if err != nil {
		return err
	}

	if tail > 0 {
		lines := bytes.Split(data, []byte("\n"))
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		fmt.Print(string(bytes.Join(lines, []byte("\n"))))
	} else {
		fmt.Print(string(data))
	}
	return nil
}

// tailFollow tails a log file in real-time, optionally starting from line N.
func tailFollow(path string, tail int) error {
	// First show last N lines if requested
	if tail > 0 {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := bytes.Split(data, []byte("\n"))
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		fmt.Print(string(bytes.Join(lines, []byte("\n"))))
	}

	// Open file and seek to end
	f, err := os.OpenFile(path, os.O_RDONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Seek to end
	stat, _ := f.Stat()
	if _, err := f.Seek(stat.Size(), io.SeekStart); err != nil {
		return err
	}

	buf := make([]byte, 4096)
	var pending []byte
	for {
		n, err := f.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			lines := bytes.Split(pending, []byte("\n"))
			// Keep last incomplete line in pending
			pending = lines[len(lines)-1]
			for _, line := range lines[:len(lines)-1] {
				if len(line) > 0 {
					fmt.Println(string(line))
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				// At end of file: poll with a delay. Continuing without
				// sleeping would busy-loop (100% CPU) on the EOF read.
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return err
		}
	}
}

func homeDir(_ string) string {
	h, _ := os.UserHomeDir()
	return h
}

// TODO(Task 10): TriggerDeploy/buildNotifier（旧编排的占位通知逻辑）已随
// notify.Send 按次传收件人的改造删除；新引擎的 email 组件接管通知。

