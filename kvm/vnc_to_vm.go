// +build windows

/*
Binary vnc_to_vm connects VNC to a remote VM via its socket file over SSH.
- connect to the local SSH agent in cygwin
- connect to the remote server with SSH
- relay the remote VNC socket file over SSH into a local listener
- start VNC viewer with it

Requirements:
- cygwin, with ssh and socat installed, ssh-agent running
- socat on the destination, TightVNC viewer locally

To avoid opening a command window:
  go build -ldflags -H=windowsgui vnc_to_vm.go
*/

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"regexp"
	"strings"

	"github.com/StalkR/winpulse/cygwin"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var (
	flagHost   = flag.String("host", "", "Host to SSH to (user@host[:port]).")
	flagSocket = flag.String("socket", "", "Path to remote VNC socket.")
	flagViewer = flag.String("viewer", `C:\Program Files\TightVNC\tvnviewer.exe`, "Path to local TightVNC viewer.")
)

func main() {
	flag.Parse()
	if err := launch(); err != nil {
		log.Fatal(err)
	}
}

func launch() error {
	if *flagHost == "" || *flagSocket == "" {
		flag.PrintDefaults()
		return nil
	}
	userHost := regexp.MustCompile(`^([^@]*)@(.*)$`).FindStringSubmatch(*flagHost)
	if len(userHost) == 0 {
		return fmt.Errorf("invalid host")
	}
	user, host := userHost[1], userHost[2]
	if strings.Contains(*flagSocket, `"`) || strings.Contains(*flagSocket, `'`) {
		return fmt.Errorf("invalid socket path")
	}
	socket := *flagSocket

	vnc, err := vncViewer()
	if err != nil {
		return err
	}
	defer vnc.Close()
	return sshToVNC(user, host, socket, vnc)
}

func vncViewer() (io.ReadWriteCloser, error) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	cmd := exec.Command(*flagViewer, fmt.Sprintf("localhost::%d", port))
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	conn, err := ln.Accept()
	if err != nil {
		cmd.Wait()
		return nil, err
	}
	if err := ln.Close(); err != nil {
		cmd.Wait()
		return nil, err
	}
	log.Printf("VNC viewer connected (localhost:%v)", port)
	return &vncConn{conn, cmd}, nil
}

// vncConn implements io.ReadWriteCloser.
type vncConn struct {
	conn io.ReadWriteCloser
	cmd  *exec.Cmd
}

func (s *vncConn) Read(p []byte) (int, error)  { return s.conn.Read(p) }
func (s *vncConn) Write(p []byte) (int, error) { return s.conn.Write(p) }
func (s *vncConn) Close() error {
	var errors []error
	if err := s.conn.Close(); err != nil {
		errors = append(errors, err)
	}
	if err := s.cmd.Wait(); err != nil {
		errors = append(errors, err)
	}
	if len(errors) > 0 {
		if len(errors) == 1 {
			return errors[0]
		}
		return fmt.Errorf("close: %v errors, first: %v", len(errors), errors[0])
	}
	return nil
}

func sshToVNC(user, host, socket string, vnc io.ReadWriter) error {
	client, err := connect(user, host)
	if err != nil {
		return err
	}
	defer client.Close()
	log.Print("Connected to SSH server")
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	session.Stdin = vnc
	session.Stdout = vnc
	if err := session.Start(fmt.Sprintf(`socat 'UNIX:"%s"' -`, socket)); err != nil {
		return err
	}
	log.Print("Connected to VNC via SSH")
	return session.Wait()
}

func connect(user, host string) (*ssh.Client, error) {
	ag, err := cygwin.SSHAgent()
	if err != nil {
		return nil, err
	}
	defer ag.Close()
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeysCallback(agent.NewClient(ag).Signers),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	return ssh.Dial("tcp", host, config)
}
