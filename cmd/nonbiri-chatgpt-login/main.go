// nonbiri-chatgpt-login authorizes a locally run client and writes an encrypted
// connection envelope that only the selected NonbiriAPI instance can import.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/chatgptauth"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

func readFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("input file is too large or unreadable")
	}
	return raw, nil
}
func writeNew(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func run() error {
	targetPath := flag.String("target", "", "Instance target JSON downloaded from the administrator page")
	outputPath := flag.String("output", "nonbiri-chatgpt-connection.json", "New encrypted connection file; existing files are never overwritten")
	hostPath := flag.String("host-id-file", "nonbiri-chatgpt-host-id.txt", "Persistent opaque identifier for this local computer")
	flag.Parse()
	if *targetPath == "" {
		return errors.New("--target is required")
	}
	if _, err := os.Stat(*outputPath); err == nil {
		return errors.New("output already exists; select a new path")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := readFile(*targetPath, 8192)
	if err != nil {
		return err
	}
	var target chatgptauth.Target
	if json.Unmarshal(raw, &target) != nil || target.Version != 1 {
		return errors.New("invalid instance target")
	}
	localHost := ""
	raw, err = readFile(*hostPath, 256)
	if errors.Is(err, os.ErrNotExist) {
		localHost, err = chatgptauth.NewHostID()
		if err == nil {
			err = writeNew(*hostPath, []byte(localHost+"\n"))
		}
	} else if err == nil {
		localHost = strings.TrimSpace(string(raw))
	}
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	stack, err := egress.NewStack(egress.StackOptions{})
	if err != nil {
		return err
	}
	defer stack.CloseIdleConnections()
	if err = stack.AddSelfOrigins(ctx, &config.Config{SiteBaseURL: "http://127.0.0.1:1", UserHost: "127.0.0.1", AdminHost: "127.0.0.1", ListenAddr: "127.0.0.1:1"}); err != nil {
		return err
	}
	envelope, err := chatgptauth.NewClient(stack).Login(ctx, target, localHost, func(link string) error {
		_, err := fmt.Fprintf(os.Stderr, "Open this link in a browser on this computer and approve NonbiriAPI:\n%s\n", link)
		return err
	})
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err = writeNew(*outputPath, encoded); err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stderr, "Encrypted connection file created. Import it into the selected instance over HTTPS, then remove the transfer file.")
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Authorization could not be completed:", err)
		os.Exit(1)
	}
}
