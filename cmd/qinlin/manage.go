package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"
	"qinlin/internal/config"
	"qinlin/internal/supervisor"
	"qinlin/internal/version"
)

func runCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: qinlin [init | admin show/set/reset | config show/set-port | check-config | --version]")
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("qinlin-server " + version.Version + " (" + version.Revision + ")")
		return nil
	}
	if len(args) == 1 && args[0] == "--version-json" {
		return json.NewEncoder(os.Stdout).Encode(version.Current())
	}
	command := args[0]
	sub := ""
	if command == "admin" || command == "config" {
		if len(args) < 2 {
			return errors.New("missing management subcommand")
		}
		sub = args[1]
		args = args[1:]
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	path := fs.String("config", config.Path(), "configuration file path")
	var addr, publicURL, db *string
	var mock *bool
	var port *int
	if command == "init" {
		addr = fs.String("addr", "", "listen address")
		publicURL = fs.String("public-url", "", "public HTTP or HTTPS origin")
		db = fs.String("db", "", "SQLite path")
		mock = fs.Bool("mock", false, "use local mock doors")
	}
	if command == "config" && sub == "set-port" {
		port = fs.Int("port", 0, "new listening port")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected command arguments")
	}
	switch command {
	case "supervise":
		if err := os.Setenv("QINLIN_CONFIG", *path); err != nil {
			return err
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return supervisor.Run(ctx, cfg)
	case "init":
		values := map[string]string{}
		if *addr != "" {
			values["QINLIN_ADDR"] = *addr
		}
		if *publicURL != "" {
			values["QINLIN_PUBLIC_URL"] = *publicURL
		}
		if *db != "" {
			values["QINLIN_DB"] = *db
		}
		if *mock {
			values["QINLIN_MOCK"] = "true"
		}
		if err := config.InitFile(*path, values); err != nil {
			return err
		}
		fmt.Println("配置已创建；原配置不会被覆盖。可运行 admin set 或 admin reset 设置后台账号密码。")
		return nil
	case "admin":
		switch sub {
		case "show":
			return showConfig(*path)
		case "reset":
			username, password, err := config.ResetAdmin(*path)
			if err != nil {
				return err
			}
			fmt.Printf("新的管理员账号：%s\n新的管理员密码：%s\n请记录以上凭据；配置中仅保存密码哈希。重启服务后生效。\n", username, password)
			return nil
		case "set":
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("请在交互式终端中设置账号密码，密码输入将隐藏")
			}
			fmt.Fprint(os.Stderr, "新管理员账号：")
			username, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil {
				return err
			}
			fmt.Fprint(os.Stderr, "新管理员密码（12～72 字节，输入隐藏）：")
			password, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			fmt.Fprint(os.Stderr, "再次输入密码：")
			confirmation, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			if string(password) != string(confirmation) {
				return errors.New("两次输入的密码不一致，配置未修改")
			}
			if err = config.SetAdmin(*path, strings.TrimSpace(username), string(password)); err != nil {
				return err
			}
			fmt.Println("管理员凭据已更新；重启服务后生效。亲邻会话和门禁令牌均已保留。")
			return nil
		}
	case "config":
		if sub == "show" {
			return showConfig(*path)
		}
		if sub == "set-port" {
			if err := config.SetPort(*path, *port); err != nil {
				return err
			}
			return showConfig(*path)
		}
	case "check-config":
		values, err := config.ReadFile(*path)
		if err != nil {
			return err
		}
		if _, err = config.ParseFileValues(values); err != nil {
			return err
		}
		fmt.Println("配置检查通过")
		return nil
	}
	return errors.New("unknown management command")
}
func showConfig(path string) error {
	values, err := config.ReadFile(path)
	if err != nil {
		return err
	}
	cfg, err := config.ParseFileValues(values)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"addr": cfg.Addr, "public_url": cfg.PublicURL, "db": cfg.DB, "username": cfg.AdminUsername, "password_configured": cfg.AdminPasswordHash != "", "mock": cfg.Mock})
}
