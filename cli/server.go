package cli

import (
	"fmt"
	"layerd/Interfaces"
	"layerd/PeerManager"
	"log"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

func echoServer(c net.Conn) {
	_, _ = c.Write([]byte(fmt.Sprintf("layerd cli\nCommands:\n routes show\n withdraw <prefix>@<l3id>\n announce <prefix>@<l3id>\nMy IPV6: %s\nMy node ID: %s\n", PeerManager.MyIPv6.String(), PeerManager.NodeIDToString(PeerManager.MyNodeID))))
	for {
		buf := make([]byte, 1000)
		nr, err := c.Read(buf)
		if err != nil {
			return
		}

		data := string(buf[0:nr])
		cmd := strings.Split(strings.TrimSpace(data), " ")
		if len(cmd) != 2 {
			_, _ = c.Write([]byte("unknown command\n"))
			continue
		}
		if cmd[0] == "announce" {
			d := strings.Split(cmd[1], "@")
			if len(d) != 2 {
				_, _ = c.Write([]byte("incomplete command\n"))
				continue
			}
			l3id, err := strconv.Atoi(d[1])
			if err != nil {
				_, _ = c.Write([]byte("l3id is not an int value\n"))
				continue
			}
			addr, err := netip.ParsePrefix(d[0])
			if err != nil {
				_, _ = c.Write([]byte(err.Error() + "\n"))
				continue
			}
			PeerManager.AddAnnouncement(uint8(l3id), addr)
			_, _ = c.Write([]byte("OK\n"))
		} else if cmd[0] == "withdraw" {
			d := strings.Split(cmd[1], "@")
			if len(d) != 2 {
				_, _ = c.Write([]byte("incomplete command\n"))
				continue
			}
			l3id, err := strconv.Atoi(d[1])
			if err != nil {
				_, _ = c.Write([]byte("l3id is not an int value\n"))
				continue
			}
			addr, err := netip.ParsePrefix(d[0])
			if err != nil {
				_, _ = c.Write([]byte(err.Error() + "\n"))
				continue
			}
			PeerManager.WithdrawAnnouncement(uint8(l3id), addr)
			_, _ = c.Write([]byte("OK\n"))
		} else if cmd[0] == "routes" {
			final := ""
			for i := 0; i < len(Interfaces.LookupArray); i++ {
				tDst := Interfaces.LookupArray[i].Load()
				if tDst == nil {
					continue
				}
				destinations := *tDst
				for s := 0; s < len(destinations); s++ {
					final = final + fmt.Sprintln("Updated route", destinations[s].Prefix.String(), "nextHop", destinations[s].NextHop.String(), "l3id", i)
				}
			}
			if final == "" {
				final = "No routes"
			}
			_, _ = c.Write([]byte(final + "\n"))

		}

	}
}

func Startup() {
	_ = os.Remove("cli.sock")
	l, err := net.Listen("unix", "cli.sock")
	if err != nil {
		log.Fatal("listen error:", err)
	}

	for {
		fd, err := l.Accept()
		if err != nil {
			log.Println("accept error:", err)
		}
		go echoServer(fd)
	}
}
