package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/bi-zone/etw"
	"github.com/kardianos/service"
	"golang.org/x/sys/windows"
	"gopkg.in/yaml.v2"
)

// Struct to be completed when config file is read
type conf struct {
	Timeout   int
	Ignored   []string
	LogToFile bool
}

type program struct{}

const name = "sessioncleaner"
const serviceName = "Remote Desktop Session Cleaner"
const serviceDescription = "It closes disconnected RDP sessions"

var (
	config  conf
	session *etw.Session
)

func main() {
	// cd to working directory, so we can use relative paths after
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		fmt.Printf("Start Folder not found\r\n")
	} else {
		err := os.Chdir(dir)
		if err != nil {
			fmt.Printf("Start Folder not found\r\n")
		}
	}
	//Reading configuration from yaml file
	config.getConf()

	// Logging
	if config.LogToFile {
		file, err := os.OpenFile(name+".log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0776)
		if err != nil {
			log.Printf("Log file error: %v\r\n ", err)
		}
		log.SetOutput(file)
	}

	//Service Structure
	serviceConfig := &service.Config{
		Name:        name,
		DisplayName: serviceName,
		Description: serviceDescription,
	}
	prg := &program{}

	s, err := service.New(prg, serviceConfig)
	if err != nil {
		log.Printf("Error initializing service: %v\r\n", err)
	}

	// Get arguments if started from cmd...
	contargs := len(os.Args)
	if contargs > 1 {
		argumentos := os.Args[1:]
		for _, param := range argumentos {

			switch param {
			case "install":
				err = s.Install()
				if err != nil {
					fmt.Println(err)
				} else {
					fmt.Println("Service installed correctly")
					os.Exit(0)
				}
			case "uninstall":
				err = s.Uninstall()
				if err != nil {
					fmt.Println(err)
				} else {
					fmt.Println("Service removed correctly")
					os.Exit(0)
				}
			}
		}
	} else {
		err = s.Run() //... if there is no arguments, just starts normally
		if err != nil {
			log.Printf("Error starting service: %v\r\n", err)
		}
	}
}

func (c *conf) getConf() *conf {
	yamlFile, err := ioutil.ReadFile("config.yml")
	if err != nil {
		log.Printf("Error reading config.yml: #%v \r\n", err)
	}
	err = yaml.Unmarshal(yamlFile, c)
	if err != nil {
		log.Fatalf("Error reading config: %v", err)
	}
	return c
}

func (p *program) Start(s service.Service) error {
	log.Println(s.String() + " Started")
	go p.run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	err := session.Close()
	if err != nil {
		log.Println("Error unsuscribing the ETW: ", err)
	}
	log.Println(s.String() + " Stopped")
	return nil
}

//Here starts the program work
func (p *program) run() {
	session := createETWSession()
	go startETWListener(session)
}

func createETWSession() *etw.Session {
	var err error
	// GUID obtained from "logman query providers" command
	guid, _ := windows.GUIDFromString("{5D896912-022D-40AA-A3A8-4FA5515C76D7}") //GUID of the "Microsoft-Windows-TerminalServices-LocalSessionManager" provider
	nameSession := "Service-SessionCleaner"                                     //We can check if the ETW client is running with the "logman -ets" command. We will see it with this name
	etw.KillSession(nameSession)                                                //We kill it first, just in case it already exist (It should not)
	session, err = etw.NewSession(guid, etw.WithName(nameSession))
	if err != nil {
		log.Println("Error creating the ETW session: ", err)
	}
	return session
}

func startETWListener(session *etw.Session) {
	cb := func(e *etw.Event) {
		if e.Header.ID == 24 { // 24 is the "Disconnected" event in the Event Viewer
			closeSession(e)
		}
	}

	// "session.Process" blocks until "session.Close()" is being called (see "Stop" function of the service), so is needed to be started in a Goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		if err := session.Process(cb); err != nil {
			log.Printf("Error processing the events: %s", err)
		}
		wg.Done()
	}()
	wg.Wait()
}

func closeSession(e *etw.Event) {
	data, _ := e.EventProperties()
	ignored := checkIgnored(data["User"].(string), config.Ignored) //Ignoring according to Ignored in yaml file
	if ignored {
		log.Printf("The user %s was disconnected, but it's in the Ignored list\r\n", (data["User"].(string)))
		return
	} else {
		log.Printf("The user %s with Session ID %s was disconnected. Waiting %v seconds according to the Timeout setting, and forcing logoff", data["User"], data["SessionID"], config.Timeout)
		time.Sleep(time.Duration(config.Timeout) * time.Second) //Waiting according to Timeout in yaml file
		_, err := exec.Command("logoff", data["SessionID"].(string)).Output()
		if err != nil {
			log.Println("Error in logoff: ", err)
		} else {
			log.Printf("Session ID %s succefully logged off", data["SessionID"])
		}
	}
}

func checkIgnored(userEvent string, listIgnored []string) bool {
	for _, user := range listIgnored {
		if user == userEvent {
			return true
		}
	}
	return false
}
