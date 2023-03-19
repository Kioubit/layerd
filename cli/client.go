package cli

import (
	"bufio"
	"fmt"
	"net"
	"os"
)

func StartClient() {
	c, err := net.Dial("unix", "cli.sock")
	if err != nil {
		fmt.Println("Error", err)
		os.Exit(1)
	}
	go listener(c)
	for {
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			panic(err)
		}
		_, err = c.Write([]byte(input))
		if err != nil {
			fmt.Println(err)
		}
	}
}
func listener(c net.Conn) {
	for {
		buf := make([]byte, 1000)
		nr, err := c.Read(buf)
		if err != nil {
			fmt.Println("Error listening")
			return
		}
		data := buf[0:nr]
		fmt.Println(string(data))
	}
}
