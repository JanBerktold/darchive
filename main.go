package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/abiosoft/ishell"
	"github.com/bwmarrin/discordgo"
	"io"
	"os"
	"strconv"
)

type User struct {
	Name, Pass string
	Session    *discordgo.Session
	Context    *discordgo.UserGuild
}

var (
	currentUser *User
)

func GetMessages(s *discordgo.Session, channel string) (chan *discordgo.Message, chan error) {
	resultChan := make(chan *discordgo.Message)
	errorChan := make(chan error)
	go func() {
		lastMessage := ""
		for {
			messages, err := currentUser.Session.ChannelMessages(
				channel, 100, lastMessage, "")

			if err != nil {
				errorChan <- err
				close(resultChan)
				return
			}

			for _, message := range messages {
				resultChan <- message
			}

			if len(messages) == 0 {
				close(resultChan)
				return
			}
			lastMessage = messages[len(messages)-1].ID
		}
	}()
	return resultChan, errorChan
}

// TODO: tests and documentation
// TODO: add ranges, e.g 1-6
func ParseRange(length int, args []string) ([]bool, error) {
	result := make([]bool, length)

	if len(args) == 0 {
		for i, _ := range result {
			result[i] = true
		}
	} else {
		for _, arg := range args {
			num, err := strconv.Atoi(arg)
			if err != nil {
				return result, errors.New(fmt.Sprintf("%q is not a valid channel #"))
			}

			if num < 0 || num > length {
				return result, errors.New(fmt.Sprintf("%q is not a valid channel #, outside of range"))
			}

			result[num] = true
		}
	}

	return result, nil
}

func main() {
	shell := ishell.New()

	shell.AddCmd(&ishell.Cmd{
		Name: "login",
		Func: func(c *ishell.Context) {

			c.ShowPrompt(false)
			defer c.ShowPrompt(true)

			c.Print("Username: ")
			username := c.ReadLine()

			c.Print("Password: ")
			password := c.ReadPassword()

			session, err := discordgo.New(username, password)
			if err != nil {
				c.Printf("error while logging: %q\n", err)
				return
			}

			currentUser = &User{
				Name:    username,
				Pass:    password,
				Session: session,
			}

		},
	})

	shell.AddCmd(&ishell.Cmd{
		Name: "logout",
		Func: func(c *ishell.Context) {
			if currentUser == nil {
				c.Println("not logged in")
			}

			currentUser = nil
		},
	})

	shell.AddCmd(&ishell.Cmd{
		Name: "list",
		Func: func(c *ishell.Context) {
			if currentUser == nil {
				c.Println("not logged in")
				return
			}

			if currentUser.Context == nil {

				// TODO: Currently assuming that the guilds order does not change between requests.
				if guilds, err := currentUser.Session.UserGuilds(); err == nil {
					for i, guild := range guilds {
						c.Printf("%d - %s\n", i, guild.Name)
					}
				} else {
					c.Printf("error getting guilds: %q\n", err)
				}
			} else {

				if channels, err := currentUser.Session.GuildChannels(currentUser.Context.ID); err == nil {
					for i, channel := range channels {
						c.Printf("%d - %s\n", i, channel.Name)
					}
				} else {
					c.Printf("error getting guild channels: %q\n", err)
				}
			}
		},
	})

	shell.AddCmd(&ishell.Cmd{
		Name: "archive",
		Func: func(c *ishell.Context) {
			if currentUser == nil {
				c.Println("not logged in")
				return
			}

			if currentUser.Context == nil {
				c.Println("not within the scope of a guild")
				return
			}

			c.Printf("Output file (without extension): ")
			fileName := c.ReadLine() + ".zip"

			if _, err := os.Stat(fileName); !os.IsNotExist(err) {
				c.Printf("file already exists")
				return
			}

			file, err := os.Create(fileName)
			if err != nil {
				c.Printf("opening file: %q\n", err)
				return
			}
			defer func() {
				if err := file.Close(); err != nil {
					c.Printf("error closing file: %q\n", err)
				}
			}()

			if channels, err := currentUser.Session.GuildChannels(currentUser.Context.ID); err == nil {
				targets, err := ParseRange(len(channels), c.Args)
				if err != nil {
					c.Printf("%s\n", err.Error())
					return
				}

				c.ProgressBar().Indeterminate(true)
				c.ProgressBar().Start()

				archive := zip.NewWriter(file)

				errors := make(chan error)
				done := make(chan struct{})
				launched := 0
				for i, channel := range channels {
					if targets[i] {
						launched++

						f, err := archive.Create(channel.Name + ".json")
						if err != nil {
							c.Printf("opening zip archive: %q\n", err)
							return
						}

						go func(channel *discordgo.Channel, writer io.Writer) {
							msgChan, errChan := GetMessages(currentUser.Session, channel.ID)

							encoder := json.NewEncoder(writer)

							for {
								select {
								case msg, more := <-msgChan:
									if more {
										if err := encoder.Encode(msg); err != nil {
											errors <- err
											return
										}
									} else {
										done <- struct{}{}
										return
									}
								case err := <-errChan:
									errors <- err
									return
								}
							}
						}(channel, f)
					}
				}
				for i := 0; i < launched; i++ {
					select {
					case err := <-errors:
						c.ProgressBar().Stop()
						c.Printf("getting channel messages: %q\n", err)
						return
					case <-done:
					}
				}

				c.ProgressBar().Stop()

				if err := archive.Close(); err != nil {
					c.Printf("closing zip archive: %q\n", err)
				}

			} else {
				c.Printf("error getting guild channels: %q\n", err)
				return
			}
		},
	})

	shell.AddCmd(&ishell.Cmd{
		Name: "enter",
		Func: func(c *ishell.Context) {
			if currentUser == nil {
				c.Println("not logged in")
				return
			}

			if len(c.Args) != 1 {
				c.Println("invalid number of arguments, should be enter <guild #>")
			}

			num, err := strconv.Atoi(c.Args[0])
			if err != nil {
				c.Println("invalid guild number, should be enter <guild #>")
			}

			if guilds, err := currentUser.Session.UserGuilds(); err == nil {
				if num > len(guilds) || num < 0 {
					c.Println("invalid guild number, not within allowed range")
				}
				currentUser.Context = guilds[num]
			} else {
				c.Printf("error getting guilds: %q\n", err)
			}
		},
	})

	shell.AddCmd(&ishell.Cmd{
		Name: "leave",
		Func: func(c *ishell.Context) {
			if currentUser == nil {
				c.Println("not logged in")
				return
			}

			if currentUser.Context == nil {
				c.Println("not within the scope of a channel")
			}

			currentUser.Context = nil
		},
	})
	shell.Start()
}
