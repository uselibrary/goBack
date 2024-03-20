package main

import (
	"archive/zip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
)

type Telegram struct {
	BotToken string `json:"BotToken"`
	ChatID   int64  `json:"ChatID"`
	Enable   bool   `json:"enable"`
}

type Config struct {
	Telegram      Telegram     `json:"telegram"`
	WebsiteTasks  []BackupTask `json:"WebsiteTasks"`
	DatabaseTasks []BackupTask `json:"DatabaseTasks"`
	ConfigTasks   []BackupTask `json:"ConfigTasks"`
}

type BackupTask struct {
	Name         string `json:"Name,omitempty"`
	Website      string `json:"Website,omitempty"`
	Database     string `json:"Database,omitempty"`
	BackupSource string `json:"BackupSource"`
	StorePath    string `json:"StorePath"`
	MaxBackup    int    `json:"MaxBackup"`
	OnedrivePath string `json:"OnedrivePath"`
}

func createZip(source, target string) error {
	zipfile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipfile.Close()

	archive := zip.NewWriter(zipfile)
	defer archive.Close()

	err = filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		header.Name = filepath.Join(filepath.Base(source), path[len(source):])

		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(writer, file)
			if err != nil {
				return err
			}

		}
		return err
	})

	return err
}

func send_message(botToken string, chatID int64, message string, enable bool) {
	if !enable {
		return
	}
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatalf("Error creating Telegram bot: %v", err)
	}
	msg := tgbotapi.NewMessage(chatID, message)
	_, err = bot.Send(msg)
	if err != nil {
		log.Printf("Error sending message: %v", err)
	}
}

func backup_website(task BackupTask, botToken string, chatID int64, enable bool) {
	zip_file := task.StorePath + "/" + task.Website + "-" + time.Now().Format("20060102-150405") + ".zip"
	err := createZip(task.BackupSource, zip_file)
	if err != nil {
		send_message(botToken, chatID, "Website Backup FAILED: "+task.Website, enable)
	}
}

func backup_database(task BackupTask, botToken string, chatID int64, enable bool) {
	backup_file := task.Database + "-" + time.Now().Format("20060102-150405") + ".sql"
	mysqldump_command := "mysqldump " + task.Database + " > " + task.StorePath + "/" + backup_file
	if _, err := exec.Command("sh", "-c", mysqldump_command).Output(); err != nil {
		send_message(botToken, chatID, "Database Backup FAILED: "+task.Database, enable)
	}
}

func backup_config(task BackupTask, botToken string, chatID int64, enable bool) {
	zip_file := task.StorePath + "/" + task.Name + "-" + time.Now().Format("20060102-150405") + ".zip"
	err := createZip(task.BackupSource, zip_file)
	if err != nil {
		send_message(botToken, chatID, "Config Backup FAILED: "+task.Name, enable)
	}
}

func check_backup_file_num(task BackupTask) {
	files, _ := os.ReadDir(task.StorePath)
	if len(files) > task.MaxBackup {
		sort.Slice(files, func(i, j int) bool {
			modTimeI, _ := files[i].Info()
			modTimeJ, _ := files[j].Info()
			return modTimeI.ModTime().Before(modTimeJ.ModTime())
		})
		for i := 0; i < len(files)-task.MaxBackup; i++ {
			os.Remove(task.StorePath + "/" + files[i].Name())
		}
	}
}

func copy_backup_to_onedrive(task BackupTask, botToken string, chatID int64, enable bool) {
	rclone_command := "rclone sync " + task.StorePath + " " + task.OnedrivePath
	if _, err := exec.Command("sh", "-c", rclone_command).Output(); err != nil {
		send_message(botToken, chatID, "Copy to onedrive FAILED: "+task.StorePath, enable)
	}
}

func handle_task(task BackupTask, botToken string, chatID int64, enable bool, backupFunc func(BackupTask, string, int64, bool)) {
	backupFunc(task, botToken, chatID, enable)
	check_backup_file_num(task)
	copy_backup_to_onedrive(task, botToken, chatID, enable)
}

func main() {
	configPath := flag.String("c", "", "Path to the configuration file")
	flag.Parse()

	if *configPath == "" {
		fmt.Println("Please provide a configuration file with the -c flag")
		os.Exit(1)
	}

	configFile, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}
	var config Config
	err = json.Unmarshal(configFile, &config)
	if err != nil {
		log.Fatalf("Error unmarshalling config file: %v", err)
	}

	var wg sync.WaitGroup
	for _, task := range config.WebsiteTasks {
		wg.Add(1)
		go func(task BackupTask) {
			defer wg.Done()
			handle_task(task, config.Telegram.BotToken, config.Telegram.ChatID, config.Telegram.Enable, backup_website)
		}(task)
	}
	for _, task := range config.DatabaseTasks {
		wg.Add(1)
		go func(task BackupTask) {
			defer wg.Done()
			handle_task(task, config.Telegram.BotToken, config.Telegram.ChatID, config.Telegram.Enable, backup_database)
		}(task)
	}
	for _, task := range config.ConfigTasks {
		wg.Add(1)
		go func(task BackupTask) {
			defer wg.Done()
			handle_task(task, config.Telegram.BotToken, config.Telegram.ChatID, config.Telegram.Enable, backup_config)
		}(task)
	}
	wg.Wait()
}
