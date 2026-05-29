package main

import (
    "context"
    _ "embed"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
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
    uploadedFilesKey = "already_uploaded_photos_list_pc"
    targetFolderID   = "13LUCsiPP-K9H29dPf3DRqx_AcHviYJIZ"
)

//go:embed credentials.json
var googleCredentials []byte

func main() {
    myApp := app.NewWithID("com.frost.photosync.desktop")
    myApp.Settings().SetTheme(theme.DarkTheme())

    myWindow := myApp.NewWindow("Photo Sync - PC Version")

    titleLabel := widget.NewLabelWithStyle("Синхронизация фото через ПК", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
    
    pathEntry := widget.NewEntry()
    pathEntry.SetPlaceHolder("Вставьте путь к папке")
    pathEntry.SetText("C:\\TestPhotos")

    statusLabel := widget.NewLabelWithStyle("Статус: Ожидание запуска", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

    var sendBtn *widget.Button

    sendBtn = widget.NewButtonWithIcon("Начать отправку в облако", theme.ConfirmIcon(), func() {
        targetPath := strings.TrimSpace(pathEntry.Text)
        if targetPath == "" {
            dialog.ShowError(fmt.Errorf("Укажите путь к папке!"), myWindow)
            return
        }
        sendBtn.Disable()
        go handleDesktopUploadProcess(myApp, myWindow, statusLabel, sendBtn, targetPath)
    })

    cancelBtn := widget.NewButtonWithIcon("Выход", theme.CancelIcon(), func() {
        os.Exit(0)
    })

    buttonsGrid := container.NewGridWithColumns(2, sendBtn, cancelBtn)

    popupCard := widget.NewCard("", "", container.NewVBox(
        titleLabel,
        widget.NewLabel("Путь к фотографиям:"),
        pathEntry,
        layout.NewSpacer(),
        statusLabel,
        layout.NewSpacer(),
        buttonsGrid,
    ))

    myWindow.SetContent(container.NewCenter(container.NewStack(popupCard)))
    myWindow.Resize(fyne.NewSize(450, 250))
    myWindow.ShowAndRun()
}

func handleDesktopUploadProcess(a fyne.App, w fyne.Window, statusLabel *widget.Label, sendBtn *widget.Button, cameraPath string) {
    defer fyne.Do(func() { sendBtn.Enable() })
    fyne.Do(func() { statusLabel.SetText("Авторизация в Google...") })

    ctx := context.Background()
    
    config, err := google.ConfigFromJSON(googleCredentials, drive.DriveFileScope)
    if err != nil {
        fyne.Do(func() {
            statusLabel.SetText("Ошибка конфигурации")
            dialog.ShowError(fmt.Errorf("Ошибка OAuth конфига:\n%v", err), w)
        })
        return
    }

    client := getClient(ctx, config)
    driveService, err := drive.NewService(ctx, option.WithHTTPClient(client))
    if err != nil {
        fyne.Do(func() {
            statusLabel.SetText("Ошибка API")
            dialog.ShowError(fmt.Errorf("Ошибка создания сервиса:\n%v", err), w)
        })
        return
    }

    fyne.Do(func() { statusLabel.SetText("Сканирование папки...") })
    dirEntries, err := os.ReadDir(cameraPath)
    if err != nil {
        fyne.Do(func() {
            statusLabel.SetText("Ошибка чтения пути")
            dialog.ShowError(fmt.Errorf("Не удалось прочитать папку:\n%v", err), w)
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
            fullPath := filepath.Join(cameraPath, name)
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
            statusLabel.SetText("Статус: Снимки не найдены")
            dialog.ShowInformation("Информация", "Новые снимки за сегодня не найдены.", w)
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
            fyne.Do(func() {
                statusLabel.SetText("Ошибка передачи")
                dialog.ShowError(fmt.Errorf("Ошибка отправки файла %s:\n%v", name, err), w)
            })
            return
        }
        successfullyUploaded++
        uploadedSuccessfullyNames = append(uploadedSuccessfullyNames, name)
    }

    fyne.Do(func() { statusLabel.SetText("Статус: Успешно готово!") })
    if uploadedString != "" {
        uploadedString += "," + strings.Join(uploadedSuccessfullyNames, ",")
    } else {
        uploadedString = strings.Join(uploadedSuccessfullyNames, ",")
    }
    prefs.SetString(uploadedFilesKey, uploadedString)

    fyne.Do(func() {
        msg := fmt.Sprintf("Успешно отправлено: %d шт.\nУдалить их?", successfullyUploaded)
        dialog.ShowConfirm("Готово", msg, func(deleteConfirmed bool) {
            if deleteConfirmed {
                for _, path := range photosToUpload {
                    _ = os.Remove(path)
                }
            }
        }, w)
    })
}

func getClient(ctx context.Context, config *oauth2.Config) *http.Client {
    tokFile := "token.json"
    tok, err := tokenFromFile(tokFile)
    if err != nil {
        tok = getTokenFromWeb(config)
        saveToken(tokFile, tok)
    }
    return config.Client(ctx, tok)
}

func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
    authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
    fmt.Printf("\nПерейдите по ссылке в браузере для авторизации:\n\n%v\n\n", authURL)
    fmt.Print("Введите полученный код авторизации: ")

    var authCode string
    if _, err := fmt.Scan(&authCode); err != nil {
        fmt.Printf("Ошибка ввода кода: %v\n", err)
        os.Exit(1)
    }

    tok, err := config.Exchange(context.TODO(), authCode)
    if err != nil {
        fmt.Printf("Ошибка обмена токена: %v\n", err)
        os.Exit(1)
    }
    return tok
}

func tokenFromFile(file string) (*oauth2.Token, error) {
    f, err := os.Open(file)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    tok := &oauth2.Token{}
    err = json.NewDecoder(f).Decode(tok)
    return tok, err
}

func saveToken(path string, token *oauth2.Token) {
    f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
    if err != nil {
        fmt.Printf("Ошибка сохранения токена: %v\n", err)
        os.Exit(1)
    }
    defer f.Close()
    _ = json.NewEncoder(f).Encode(token)
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
