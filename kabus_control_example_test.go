package main

import (
	"context"
	"fmt"
	"time"
)

// Example_clickKabuStationLogin_usage は [`clickKabuStationLogin()`](kabus_control.go:289) の呼び出し例を示す。
func Example_clickKabuStationLogin_usage() {
	if false {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		pid, err := clickKabuStationLogin(
			ctx,
			`C:\Users\example\AppData\Local\kabuStation\KabuS.exe`,
			60*time.Second,
			5*time.Second,
			"powershell.exe",
		)
		if err != nil {
			panic(err)
		}

		fmt.Println(pid)
	}
}

// Example_runLoginAutomation_usage は [`runLoginAutomation()`](kabus_control.go:654) の呼び出し例を示す。
func Example_runLoginAutomation_usage() {
	if false {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		if err := runLoginAutomation(ctx, 60*time.Second, 5*time.Second, "powershell.exe"); err != nil {
			panic(err)
		}
	}
}
