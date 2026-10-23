package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
	dir := flag.String("dir", ".", "database directory")
	flag.Parse()
	db, err := Open(*dir, Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	fmt.Println("GoLSM: PUT key value | GET key | DELETE key | SCAN start end [limit] | FLUSH | COMPACT | STATS | EXIT")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		parts := strings.SplitN(line, " ", 3)
		if len(parts) == 0 {
			continue
		}
		var err error
		switch strings.ToUpper(parts[0]) {
		case "PUT":
			if len(parts) != 3 {
				err = fmt.Errorf("usage: PUT key value")
			} else {
				err = db.Put(parts[1], parts[2])
			}
		case "GET":
			if len(parts) != 2 {
				err = fmt.Errorf("usage: GET key")
			} else {
				var v string
				var ok bool
				v, ok, err = db.Get(parts[1])
				if err == nil {
					if ok {
						fmt.Println(v)
					} else {
						fmt.Println("key not found")
					}
				}
			}
		case "DELETE":
			if len(parts) != 2 {
				err = fmt.Errorf("usage: DELETE key")
			} else {
				err = db.Delete(parts[1])
			}
		case "STATS":
			err = json.NewEncoder(os.Stdout).Encode(db.Stats())
		case "SCAN":
			args := strings.Fields(line)
			if len(args) < 3 || len(args) > 4 {
				err = fmt.Errorf("usage: SCAN start end [limit]; use - for unbounded")
			} else {
				start, end := args[1], args[2]
				if start == "-" {
					start = ""
				}
				if end == "-" {
					end = ""
				}
				limit := 0
				if len(args) == 4 {
					limit, err = strconv.Atoi(args[3])
				}
				if err == nil {
					var rows []Entry
					rows, err = db.Scan(start, end, limit)
					if err == nil {
						err = json.NewEncoder(os.Stdout).Encode(rows)
					}
				}
			}
		case "COMPACT":
			err = db.Compact()
		case "FLUSH":
			err = db.Flush()
		case "EXIT", "QUIT":
			return
		case "":
			continue
		default:
			err = fmt.Errorf("unknown command")
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	if err := in.Err(); err != nil && err != io.EOF {
		fmt.Fprintln(os.Stderr, err)
	}
}
