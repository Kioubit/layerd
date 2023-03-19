package main

import (
	"bufio"
	"fmt"
	"github.com/BurntSushi/toml"
	"layerd/Interfaces"
	"layerd/PeerManager"
	"layerd/cli"
	"log"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) > 1 {
		cli.StartClient()
		return
	}
	type Config struct {
		MyIPv6 string
		L3IDs  []uint8
	}
	data, err := os.ReadFile("config.ini")
	if err != nil {
		log.Fatalln("Failed opening config.ini file")
	}
	var config Config
	_, err = toml.Decode(string(data), &config)
	if err != nil {
		log.Fatalln(err)
	}

	addr, err := netip.ParseAddr(config.MyIPv6)
	if err != nil {
		panic(err)
	}

	go cli.Startup()
	Interfaces.Startup(config.L3IDs)
	PeerManager.MyIPv6 = addr
	readAnnouncements()
	PeerManager.Startup()
}

func readAnnouncements() {
	file, err := os.Open("announcements")
	if err != nil {
		fmt.Println("Could not open 'announcements' file, proceeding without. Announcements can still be added via the cli")
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		d := strings.Split(scanner.Text(), "@")
		if len(d) != 2 {
			log.Fatalln("invalid entry in announcements file")
		}
		l3id, err := strconv.Atoi(d[1])
		if err != nil {
			log.Fatalln("invalid entry in announcements file - non int value")
		}
		addr, err := netip.ParsePrefix(d[0])
		if err != nil {
			log.Fatalln(err)
		}
		PeerManager.AddAnnouncement(uint8(l3id), addr)
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}
