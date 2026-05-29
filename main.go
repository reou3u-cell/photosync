package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	uploadedFilesKey = "already_uploaded_photos_list_final_android"
	targetFolderID   = "13LUCsiPP-K9H29dPf3DRqx_AcHviYJIZ"
	
	// ВШИВАЕМ ТОКЕН ТЕКСТОМ: Скопируйте ВЕСЬ текст из вашего token.json на ПК и вставьте сюда:
	rawSavedToken = `{"access_token":"ya29.a0AQvPyIMlUVq7miKEeDaGRy_YW-veYmHdpex0rXM4Zftf5EY2XJfIcHpyYtpEUIdWZbpt6-RVMOrB31ysk9V37c8vaTNDtOZBUeb_e-10Ju7t7pqZF9vokuUKB6taX1ncUEzhE0lxBXprBy1dn0txMCqM0PMo0XlMqD88wBjBX6sFUlC7ze7xQNXrhmlI_YPhWnkfNSUaCgYKAVASARYSFQHGX2Mi1A8fRyf4brPWFBFf4n6DVw0206","token_type":"Bearer","refresh_token":"1//0czNcskZxS5DWCgYIARAAGAwSNwF-L9IrDaGWekGrGUSUNr42DMZ5Y257JA0DM2OlEruT4s3GwSri13jAnmOi1OTki-TM-jtLnFw","expiry":"2026-05-29T22:49:53.3482285+05:00","expires_in":3599}`
)

//go:embed credentials.json
var googleCredentials []byte

func main() {
	myApp := app.NewWithID("com.frost.photosync.v10")
	myApp.Settings().SetTheme(theme.DarkTheme())

	myWindow := myApp.NewWindow("Photo Sync")

	titleLabel := widget.NewLabelWithStyle("Синхронизация фото", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	descLabel := widget.NewLabelWithStyle("Автоматический сбор фото за сутки", fyne.TextAlignCenter, fyne.TextStyle{})
	statusLabel := widget.NewLabelWithStyle("Статус: Ожидание запуска", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

	var sendBtn *widget.Button

	sendBtn = widget.NewButtonWithIcon("Начать авто-отправку", theme.ConfirmIcon(), func() {
		sendBtn.Disable()
		go handleFullyAutomaticProcess(myApp, myWindow, statusLabel, sendBtn)
	})

	cancelBtn := widget.NewButtonWithIcon("Отмена", theme.CancelIcon(), func() {
		myApp.Quit()
	})

	buttonsGrid := container.NewGridWithColumns(2, sendBtn, cancelBtn)

	popupCard := widget.NewCard("", "", container.NewVBox(
		titleLabel,
		descLabel,
		statusLabel,
		layout.NewSpacer(),
		buttonsGrid,
	))

	myWindow.SetContent(container.NewCenter(container.NewStack(popupCard)))
	myWindow.Resize(fyne.NewSize(350, 200))
	myWindow.ShowAndRun()
}

func handleFullyAutomaticProcess(a fyne.App, w fyne.Window, statusLabel *widget.Label, sendBtn *widget.Button) {
	defer func() {
		fyne.Do(func() { sendBtn.Enable() })
		if r := recover(); r != nil {
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("Критический сбой: %v", r), w) })
		}
	}()

	if rawSavedToken == "{}" {
		fyne.Do(func() { dialog.ShowError(fmt.Errorf("Ошибка: Вы не вставили текст из token.json в код программы!"), w) })
		return
	}

	fyne.Do(func() { statusLabel.SetText("Авторизация в Google...") })
	ctx := context.Background()
	
	config, err := google.ConfigFromJSON(googleCredentials, drive.DriveFileScope)
	if err != nil {
		fyne.Do(func() { dialog.ShowError(fmt.Errorf("Ошибка OAuth конфигурации:\n%v", err), w) })
		return
	}

	tok := &oauth2.Token{}
	_ = json.Unmarshal([]byte(rawSavedToken), tok)
	client := config.Client(ctx, tok)

	driveService, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		fyne.Do(func() { dialog.ShowError(fmt.Errorf("Ошибка создания сервиса диска:\n%v", err), w) })
		return
	}

	fyne.Do(func() { statusLabel.SetText("Авто-поиск папки камеры...") })

	possibleCameraPaths := []string{
		"/sdcard/DCIM/Camera",
		"/storage/emulated/0/DCIM/Camera",
		os.Getenv("EXTERNAL_STORAGE") + "/DCIM/Camera",
		"/storage/emulated/0/Pictures",
	}

	var dirEntries []os.DirEntry
	var detectedPath string

	for _, path := range possibleCameraPaths {
		if path == "" {
			continue
		}
		entries, err := os.ReadDir(path)
		if err == nil && len(entries) > 0 {
			dirEntries = entries
			detectedPath = path
			break
		}
	}

	if len(dirEntries) == 0 {
		fyne.Do(func() {
			statusLabel.SetText("Папка не найдена")
			dialog.ShowError(fmt.Errorf("Android заблокировал доступ к авто-поиску папок камеры."), w)
		})
		return
	}

	prefs := a.Preferences()
	uploadedString := prefs.String(uploadedFilesKey)
	uploadedMap := make(map[string]bool)
	if uploadedString != "" {
		for _, name := range strings.Split(uploadedString, ",") {
			uploadedMap[name] = true
		}
	}

	var photosToUpload []string
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	for _, entry := range dirEntries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if uploadedMap[name] {
			continue
		}

		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
			fullPath := filepath.Join(detectedPath, name)
			info, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			if info.ModTime().After(startOfDay) {
				photosToUpload = append(photosToUpload, fullPath)
			}
		}
	}

	totalFiles := len(photosToUpload)
	if totalFiles == 0 {
		fyne.Do(func() {
			statusLabel.SetText("Статус: Снимков нет")
			dialog.ShowInformation("Информация", "Новые сегодняшние фотографии с камеры автоматически не найдены.", w)
		})
		return
	}

	successfullyUploaded := 0
	var uploadedSuccessfullyNames []string

	for i, path := range photosToUpload {
		name := filepath.Base(path)
		fyne.Do(func() { statusLabel.SetText(fmt.Sprintf("Отправка (%d из %d):\n%s", i+1, totalFiles, name)) })

		file, err := os.Open(path)
		if err != nil {
			continue
		}

		err = uploadSingleFileToGoogleDrive(driveService, file, name)
		file.Close()

		if err != nil {
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("Ошибка отправки файла %s:\n%v", name, err), w) })
			return
		}
		successfullyUploaded++
		uploadedSuccessfullyNames = append(uploadedSuccessfullyNames, name)
	}

	fyne.Do(func() { statusLabel.SetText("Статус: Успешно отправлено!") })
	
	if uploadedString != "" {
		uploadedString += "," + strings.Join(uploadedSuccessfullyNames, ",")
	} else {
		uploadedString = strings.Join(uploadedSuccessfullyNames, ",")
	}
	prefs.SetString(uploadedFilesKey, uploadedString)

	fyne.Do(func() {
		msg := fmt.Sprintf("Успешно отправлено: %d шт.\nУдалить оригиналы с телефона?", successfullyUploaded)
		dialog.ShowConfirm("Готово", msg, func(deleteConfirmed bool) {
			if deleteConfirmed {
				for _, path := range photosToUpload {
					_ = os.Remove(path)
				}
			}
			a.Quit()
		}, w)
	})
}

func uploadSingleFileToGoogleDrive(driveService *drive.Service, content io.Reader, cloudName string) error {
	ctx := context.Background()
	driveFile := &drive.File{
		Name:    cloudName,
		Parents: []string{targetFolderID},
	}
	_, err := driveService.Files.Create(driveFile).
		Context(ctx).
		Media(content, googleapi.ChunkSize(512*1024)).
		Do()
	return err
}
