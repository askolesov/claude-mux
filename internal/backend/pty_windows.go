package backend

import (
	"sync"

	gopty "github.com/aymanbagabas/go-pty"
)

// ptyProc is a process in a pseudo console (ConPTY) of its own.
type ptyProc struct {
	con   gopty.Pty
	cmd   *gopty.Cmd
	close sync.Once
	done  chan struct{}
	err   error
}

func startPty(name string, args, env []string, dir string, cols, rows uint16) (*ptyProc, error) {
	con, err := gopty.New()
	if err != nil {
		return nil, err
	}
	if err := con.Resize(int(cols), int(rows)); err != nil {
		_ = con.Close()
		return nil, err
	}
	cmd := con.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	if err := cmd.Start(); err != nil {
		_ = con.Close()
		return nil, err
	}
	p := &ptyProc{con: con, cmd: cmd, done: make(chan struct{})}
	// A pseudo console's output doesn't end when its process does: close it
	// once the process exits, so that a read returns.
	go func() {
		p.err = cmd.Wait()
		p.closeCon()
		close(p.done)
	}()
	return p, nil
}

func (p *ptyProc) Read(b []byte) (int, error)  { return p.con.Read(b) }
func (p *ptyProc) Write(b []byte) (int, error) { return p.con.Write(b) }
func (p *ptyProc) Pid() int                    { return p.cmd.Process.Pid }

// Foreground can't be read from a pseudo console.
func (p *ptyProc) Foreground() (int, bool)        { return 0, false }
func (p *ptyProc) Resize(cols, rows uint16) error { return p.con.Resize(int(cols), int(rows)) }
func (p *ptyProc) closeCon()                      { p.close.Do(func() { _ = p.con.Close() }) }

// Wait waits for the process to exit; the console is closed by then.
func (p *ptyProc) Wait() error {
	<-p.done
	return p.err
}

// Hangup does what closing a console window does: closing the pseudo
// console sends its processes CTRL_CLOSE_EVENT.
func (p *ptyProc) Hangup() { p.closeCon() }
func (p *ptyProc) Kill()   { _ = p.cmd.Process.Kill() }
