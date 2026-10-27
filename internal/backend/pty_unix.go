//go:build !windows

package backend

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// ptyProc is a process in a pseudo-terminal of its own.
type ptyProc struct {
	cmd *exec.Cmd
	f   *os.File
}

func startPty(name string, args, env []string, dir string, cols, rows uint16) (*ptyProc, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &ptyProc{cmd: cmd, f: f}, nil
}

func (p *ptyProc) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *ptyProc) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *ptyProc) Pid() int                    { return p.cmd.Process.Pid }

func (p *ptyProc) Resize(cols, rows uint16) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: cols, Rows: rows})
}

// Wait waits for the process to exit, then closes the terminal.
func (p *ptyProc) Wait() error {
	err := p.cmd.Wait()
	_ = p.f.Close()
	return err
}

// Hangup does what closing a terminal window does: SIGHUP.
func (p *ptyProc) Hangup() { _ = p.cmd.Process.Signal(syscall.SIGHUP) }
func (p *ptyProc) Kill()   { _ = p.cmd.Process.Kill() }
