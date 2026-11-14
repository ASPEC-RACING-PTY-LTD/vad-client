//go:build !windows

package main

import "errors"

var errNoHello = errors.New("Windows Hello is only available on Windows")

func helloSupported() bool { return false }

func helloCreate() (string, []byte, []byte, error) { return "", nil, nil, errNoHello }

func helloDecrypt(string, []byte) ([]byte, error) { return nil, errNoHello }

func helloDelete(string) {}
