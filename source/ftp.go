package main

import (
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Minimal passive-mode FTP client (USER/PASS/CWD/TYPE/PASV/STOR/RNFR/RNTO),
// with timeouts on every step so a dead server can never hang the program.
type ftpConn struct {
	c    net.Conn
	tp   *textproto.Conn
	host string
}

const ftpTimeout = 20 * time.Second

func ftpDial(server, user, pass string) (*ftpConn, error) {
	addr := server
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(server, "21")
	}
	c, err := net.DialTimeout("tcp", addr, ftpTimeout)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(addr)
	f := &ftpConn{c: c, tp: textproto.NewConn(c), host: host}
	if _, _, err := f.read(220); err != nil {
		c.Close()
		return nil, err
	}
	code, _, err := f.cmd(0, "USER %s", user)
	if err != nil {
		f.Close()
		return nil, err
	}
	if code == 331 {
		if _, _, err := f.cmd(230, "PASS %s", pass); err != nil {
			f.Close()
			return nil, fmt.Errorf("login failed: %v", err)
		}
	} else if code != 230 {
		f.Close()
		return nil, fmt.Errorf("USER: unexpected reply %d", code)
	}
	if _, _, err := f.cmd(200, "TYPE I"); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (f *ftpConn) read(expect int) (int, string, error) {
	f.c.SetDeadline(time.Now().Add(ftpTimeout))
	code, msg, err := f.tp.ReadResponse(expect)
	return code, msg, err
}

func (f *ftpConn) cmd(expect int, format string, a ...any) (int, string, error) {
	f.c.SetDeadline(time.Now().Add(ftpTimeout))
	if err := f.tp.PrintfLine(format, a...); err != nil {
		return 0, "", err
	}
	if expect == 0 {
		code, msg, err := f.tp.ReadResponse(0)
		if _, ok := err.(*textproto.Error); ok {
			err = nil
		}
		return code, msg, err
	}
	return f.read(expect)
}

func (f *ftpConn) Cwd(dir string) error {
	if dir == "" {
		return nil
	}
	_, _, err := f.cmd(250, "CWD %s", dir)
	return err
}

func (f *ftpConn) pasv() (net.Conn, error) {
	_, msg, err := f.cmd(227, "PASV")
	if err != nil {
		return nil, err
	}
	s, e := strings.Index(msg, "("), strings.Index(msg, ")")
	if s < 0 || e < s {
		return nil, fmt.Errorf("bad PASV reply %q", msg)
	}
	p := strings.Split(msg[s+1:e], ",")
	if len(p) != 6 {
		return nil, fmt.Errorf("bad PASV reply %q", msg)
	}
	hi, _ := strconv.Atoi(p[4])
	lo, _ := strconv.Atoi(p[5])
	// Use the control connection's host: servers behind NAT often report a
	// private address in the PASV reply.
	return net.DialTimeout("tcp", net.JoinHostPort(f.host, strconv.Itoa(hi*256+lo)), ftpTimeout)
}

func (f *ftpConn) Stor(name string, r io.Reader) error {
	dc, err := f.pasv()
	if err != nil {
		return err
	}
	code, msg, err := f.cmd(0, "STOR %s", name)
	if err != nil {
		dc.Close()
		return err
	}
	if code < 100 || code >= 200 {
		dc.Close()
		return fmt.Errorf("STOR %s: %d %s", name, code, msg)
	}
	dc.SetDeadline(time.Now().Add(2 * time.Minute))
	_, cerr := io.Copy(dc, r)
	dc.Close()
	if _, _, err := f.read(226); err != nil {
		// some servers answer 250
		if te, ok := err.(*textproto.Error); !ok || te.Code != 250 {
			return err
		}
	}
	return cerr
}

func (f *ftpConn) Rename(from, to string) error {
	if _, _, err := f.cmd(350, "RNFR %s", from); err != nil {
		return err
	}
	if _, _, err := f.cmd(250, "RNTO %s", to); err != nil {
		// IIS and some others refuse to rename over an existing file
		f.cmd(0, "DELE %s", to)
		if _, _, err2 := f.cmd(350, "RNFR %s", from); err2 != nil {
			return err
		}
		_, _, err = f.cmd(250, "RNTO %s", to)
		return err
	}
	return nil
}

func (f *ftpConn) Close() {
	f.c.SetDeadline(time.Now().Add(5 * time.Second))
	f.tp.PrintfLine("QUIT")
	f.c.Close()
}
