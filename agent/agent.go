package agent

import (
	"fmt"
	"io"
	"net"

	"golang.org/x/crypto/ssh"
)

type Agent struct {
	config *ssh.ServerConfig
}

func NewAgent(config *ssh.ServerConfig) *Agent {
	return &Agent{
		config: config,
	}
}

func (agent *Agent) Serve(l net.Listener) {
	for {
		agent.ServeOne(l, true)
	}
}

func (agent *Agent) ServeOne(l net.Listener, async bool) {
	c, err := l.Accept()
	if err != nil {
		fmt.Printf("failed to accept: %s\n", err)
		return
	}

	conn, chans, reqs, err := ssh.NewServerConn(c, agent.config)
	if err != nil {
		fmt.Printf("handshake failed: %s\n", err)
		return
	}

	if async {
		go agent.handleConn(conn, chans, reqs)
	} else {
		agent.handleConn(conn, chans, reqs)
	}
}

func (agent *Agent) handleConn(conn *ssh.ServerConn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) {
	defer conn.Close()

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			fmt.Printf("rejecting unknown channel type: %s\n", newChannel.ChannelType())
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			fmt.Printf("failed to accept channel: %s\n", err)
			return
		}

		defer channel.Close()

		for req := range requests {
			if req.Type != "exec" {
				fmt.Printf("rejecting non-exec channel request (type=%s)\n", req.Type)
				req.Reply(false, nil)
				continue
			}

			request, err := ParseRequest(req)
			if err != nil {
				fmt.Printf("%s\n", err)
				req.Reply(false, nil)
				continue
			}

			//fmt.Printf("got an agent-request [%s]\n", request.JSON)
			req.Reply(true, nil)

			// drain output to the SSH channel stream
			output := make(chan string)
			done := make(chan int)
			go func(out io.Writer, in chan string, done chan int) {
				for {
					s, ok := <-in
					if !ok {
						break
					}
					fmt.Fprintf(out, "%s", s)
				}
				close(done)
			}(channel, output, done)

			// run the agent request
			err = request.Run(output)
			<-done
			rc := []byte{0, 0, 0, 0}
			if err != nil {
				rc[0] = 1
			}
			channel.SendRequest("exit-status", false, rc)
			channel.Close()
		}
	}
}
