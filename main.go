package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gen2brain/beeep"
	"github.com/getlantern/systray"
	"gopkg.in/yaml.v2"
)

type Config struct {
	DNSAddresses        []string `yaml:"dns_addresses"`
	RunOnStartup        bool     `yaml:"run_on_startup"`
	ChangeIntervalHours int      `yaml:"change_interval_hours"`
	NotifyUser          bool     `yaml:"notify_user"`
}

var config Config
var currentDNSIndex int
var debugMode bool
var appIcon []byte

func main() {

	if len(os.Args) > 1 && os.Args[1] == "--debug" {
		debugMode = true
		log.Println("Debug mode enabled")
	}

	err := checkAdmin()
	if err != nil {
		log.Fatalf("Admin check failed: %v", err)
	}

	// Beep can get the icon without doing this, but systray is annoying and needs the byte data of the icon ready
	appIcon = getIcon("icon.ico")

	err = readConfig()
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}

	if runtime.GOOS == "windows" {
		ifaces, err := getActiveWindowsInterfaces()
		if err != nil {
			log.Fatalf("Failed to get active interfaces: %v", err)
		}
		log.Println("Active interfaces:", ifaces)
	}

	if runtime.GOOS == "darwin" {
		log.Println("Oi, quick note: I don't use a Mac, this is a toy project of mine to learn Golang. If you encounter issues with this, don't complain about it because I literally cannot fix it for you.")
	}

	if config.RunOnStartup {
		err = setRunOnStartup()
		if err != nil {
			log.Fatalf("Failed to set run on startup: %v", err)
		}
	}

	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(appIcon)
	systray.SetTitle("DNS Changer")
	systray.SetTooltip("DNS Changer running")

	mChange := systray.AddMenuItem("Change DNS", "Cycle to the next DNS server if you're having trouble with the current one.")
	mQuit := systray.AddMenuItem("Quit", "Quit the application")

	var ticker *time.Ticker
	if debugMode {
		ticker = time.NewTicker(10 * time.Second)
	} else {
		ticker = time.NewTicker(time.Duration(config.ChangeIntervalHours) * time.Hour)
	}
	defer ticker.Stop()
	Tick()

	for {
		select {
		case <-mQuit.ClickedCh:
			systray.Quit()
			os.Exit(1)
		case <-mChange.ClickedCh:
			err := changeDNS()
			if err != nil {
				beeep.Alert("DNS Change Error", err.Error(), "icon.ico")
				os.Exit(1)
			}
		case <-ticker.C:
			Tick()
		}
	}
}

func Tick() {
	err := changeDNS()
	if err != nil && config.NotifyUser {
		beeep.Alert("DNS Change Error", err.Error(), "icon.ico")
		os.Exit(1) // something clearly went wrong. Just exit instead of fucking something up.
	}
	if debugMode {
		log.Println("Tick.") // Tick
	}
}

func onExit() {
	// idk what to clean up here. Maybe revert to the last DNS before starting this?
	if debugMode {
		log.Println("Exiting the application")
	}
}

func checkAdmin() error {

	if debugMode {
		log.Println("OS: " + runtime.GOOS)
	}

	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("net", "session")
		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("this program must be run as an administrator")
		}
	case "linux", "darwin":
		if os.Geteuid() != 0 {
			return fmt.Errorf("this program must be run as root")
		}
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
	return nil
}

func readConfig() error {
	data, err := os.ReadFile("config.yaml")
	if os.IsNotExist(err) {
		err = generateDefaultConfig()
		if err != nil {
			return err
		}
		data, err = os.ReadFile("config.yaml")
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} // this hurts my soul

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return err
	}

	if config.ChangeIntervalHours <= 0 {
		config.ChangeIntervalHours = 6
	}
	return nil
}

func changeDNS() error {
	if len(config.DNSAddresses) == 0 {
		return fmt.Errorf("no DNS addresses specified in config")
	}

	currentDNS := config.DNSAddresses[currentDNSIndex]
	currentDNSIndex = (currentDNSIndex + 1) % len(config.DNSAddresses)

	var allErrors []string

	switch runtime.GOOS {
	case "windows": // fuck you
		interfaces, err := getActiveWindowsInterfaces()
		if err != nil {
			return err
		}
		for _, iface := range interfaces {
			cmd := exec.Command("powershell", "Set-DnsClientServerAddress", "-InterfaceAlias", iface, "-ServerAddresses", currentDNS)
			output, err := cmd.CombinedOutput()
			if err != nil {
				errMsg := fmt.Sprintf("Error changing DNS for interface %s to %s: %v. Output: %s", iface, currentDNS, err, string(output))
				allErrors = append(allErrors, errMsg)
				if debugMode {
					log.Println(errMsg)
				}
			} else if debugMode {
				log.Printf("Changed DNS for interface %s to %s. Output: %s", iface, currentDNS, string(output))
			}
		}
	case "linux": // THE GOAT
		cmd := exec.Command("sh", "-c", fmt.Sprintf("echo 'nameserver %s' | sudo tee /etc/resolv.conf", currentDNS))
		if debugMode {
			log.Printf("Setting DNS on Linux to %s", currentDNS)
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			errMsg := fmt.Sprintf("Error setting DNS on Linux to %s: %v. Output: %s", currentDNS, err, string(output))
			allErrors = append(allErrors, errMsg)
			if debugMode {
				log.Println(errMsg)
			}
		}
	case "darwin": //shitos
		cmd := exec.Command("networksetup", "-setdnsservers", "Wi-Fi", currentDNS)
		output, err := cmd.CombinedOutput()
		if err != nil {
			errMsg := fmt.Sprintf("Error setting DNS on macOS to %s: %v. Output: %s", currentDNS, err, string(output))
			allErrors = append(allErrors, errMsg)
			if debugMode {
				log.Println(errMsg)
			}
		}
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if len(allErrors) > 0 {
		return fmt.Errorf(strings.Join(allErrors, "\n"))
	}

	if config.NotifyUser {
		err := beeep.Notify("DNS Change", fmt.Sprintf("DNS has been changed to %s", currentDNS), "")
		if err != nil {
			return err
		}
	}
	return nil
}

func getActiveWindowsInterfaces() ([]string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive",
		"Get-NetAdapter | Where-Object { $_.Status -eq 'Up' } | Select-Object -ExpandProperty Name") // thanks ChatGPT, if I had to go through more powershell errors I would have gone insane.
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error executing PowerShell command: %v, output: %s", err, output)
		return nil, fmt.Errorf("error executing PowerShell command: %v, output: %s", err, output)
	}
	interfaces := strings.Fields(string(output))
	return interfaces, nil
}

func getIcon(s string) []byte {
	b, err := os.ReadFile(s)
	if err != nil {
		log.Fatal("TF2 COCONUT CRASH ", err)
	}
	return b
}

func generateDefaultConfig() error {
	defaultConfig := Config{
		DNSAddresses:        []string{"1.1.1.1", "1.0.0.1", "9.9.9.9"},
		RunOnStartup:        true,
		ChangeIntervalHours: 6,
		NotifyUser:          true,
	}

	data, err := yaml.Marshal(&defaultConfig)
	if err != nil {
		return err
	}

	err = os.WriteFile("config.yaml", data, 0644)
	if err != nil {
		return err
	}

	return nil
}

func setRunOnStartup() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}

	switch runtime.GOOS {
	case "windows":
		script := fmt.Sprintf(`
$path = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
$name = 'DNSChanger'
$value = '%s'

if (Get-ItemProperty -Path $path -Name $name -ErrorAction SilentlyContinue) {
    Set-ItemProperty -Path $path -Name $name -Value $value
} else {
    New-ItemProperty -Path $path -Name $name -Value $value -PropertyType String
}
`, exePath) // why

		cmd := exec.Command("powershell", "-Command", script)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to set run on startup in registry: %v. Output: %s", err, string(output))
		}
	case "linux", "darwin":
		// Create a .desktop file in ~/.config/autostart
		desktopFileContent := fmt.Sprintf(`[Desktop Entry]
Type=Application
Exec=%s
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
Name=DNSChanger
Comment=Start DNSChanger on startup
`, exePath)

		autostartDir := filepath.Join(os.Getenv("HOME"), ".config", "autostart")
		err := os.MkdirAll(autostartDir, 0755)
		if err != nil {
			return fmt.Errorf("failed to create autostart directory: %v", err)
		}

		desktopFilePath := filepath.Join(autostartDir, "DNSChanger.desktop")
		err = os.WriteFile(desktopFilePath, []byte(desktopFileContent), 0644)
		if err != nil {
			return fmt.Errorf("failed to write .desktop file: %v", err)
		}
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS) // can TempleOS even compile Go programs?
	}
	return nil
}
