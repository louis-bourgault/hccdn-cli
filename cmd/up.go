package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
	"github.com/louis-bourgault/hccdn-cli/db"
	"github.com/louis-bourgault/hccdn-cli/img"
	"github.com/louis-bourgault/hccdn-cli/types"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "upload file or directory",
	Long:  `You can upload files or directories. You can also use the -r flag to upload directories recursively.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := godotenv.Load()
		if err != nil {
			fmt.Println("theres a problem")
		}
		apiKey := os.Getenv("HCCDN_API_KEY")
		fmt.Printf("api key: %s\n", apiKey)
		if apiKey == "" {
			return fmt.Errorf("no api key in HCCDN_API_KEY")
		}
		fmt.Println("up called")
		if len(args) == 1 {
			location := args[0]
			fmt.Printf("uploading %s\n", location)
			location, err := filepath.Abs(filepath.Clean(location))
			if err != nil {
				return err
			}
			optimiseSettings := []string{}
			opt, err := cmd.Flags().GetString("optimise")
			if err != nil {
				return err
			}
			if opt == "" {
				fmt.Printf("optimising to: %s\n", opt)
				optimiseSettings = append(optimiseSettings, "none")
			} else {
				specifiedOpts := bytes.Split([]byte(opt), []byte(","))
				for _, o := range specifiedOpts {
					optimiseSettings = append(optimiseSettings, string(o))
				}
			}
			info, err := os.Stat(location)
			if err != nil {
				return err
			}
			filesToUpload := []string{}
			if info.IsDir() {
				fmt.Println("direc")
				//not recursive
				entries, err := os.ReadDir(location)
				if err != nil {
					return err
				}
				for _, entry := range entries {
					if !entry.IsDir() {
						filesToUpload = append(filesToUpload, filepath.Join(location, entry.Name()))
					}
				}
				fmt.Printf("uploading %d files\n", len(filesToUpload))
			} else {
				fmt.Println("file")
				filesToUpload = append(filesToUpload, location)
			}
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			sessionID, err := db.BeginSession(cmd.CalledAs(), wd)

			//upload the things here
			uploadedFiles := []types.File{}
			for _, file := range filesToUpload {
				optimisedUploads := []*types.Upload{}
				ext := filepath.Ext(file)
				if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
					for _, opt := range optimiseSettings {
						if !(opt == "none" || opt == "full" || opt == "") {
							input, err := os.Open(file)
							if err != nil {
								return nil
							}
							defer input.Close()
							inputImg, _, err := image.Decode(input)
							qualInt, _ := strconv.Atoi(opt)
							imageWidth := inputImg.Bounds().Dx()
							imageHeight := inputImg.Bounds().Dy()
							if qualInt >= imageWidth && qualInt >= imageHeight {
								fmt.Printf("skipping optimisation for %s at qual %s because it's larger than the original image\n", file, opt)
								continue
							}
						}

						upload, err := UploadToCDN(file, apiKey, opt)
						db.PutFile(file)

						if err != nil {
							return err
						}

						upload.FileLoc = file

						err = db.SaveUpload(upload, sessionID)
						if err != nil {
							return err
						}
						optimisedUploads = append(optimisedUploads, upload)
					}

				} else {
					upload, err := UploadToCDN(file, apiKey, "none")
					db.PutFile(file)

					if err != nil {
						return err
					}
					upload.FileLoc = file

					err = db.SaveUpload(upload, sessionID)
					if err != nil {
						return err
					}
					optimisedUploads = append(optimisedUploads, upload)
				}

				uploadedFiles = append(uploadedFiles, types.File{
					Uploads: optimisedUploads,
					Path:    file,
				})
			}

			j, err := json.Marshal(uploadedFiles)
			if err != nil {
				return err
			}
			fmt.Printf("uploaded files: %s\n", string(j))
			fmt.Printf("session id: %s\n", sessionID)
			var jsonFileLog string

			if info.IsDir() {
				jsonFileLog = filepath.Join(location, sessionID+".hccdn.json")
			} else {
				jsonFileLog = location + ".hccdn.json"
			}

			err = os.WriteFile(jsonFileLog, j, 0644)
			if err != nil {
				return err
			}

		}
		return nil
	},
}

func UploadToCDN(filePath, apiKey string, opt string) (*types.Upload, error) {

	var part io.Writer
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	defer file.Close()

	if opt == "" || opt == "none" {
		part, err = writer.CreateFormFile("file", filepath.Base(filePath))

	} else if opt == "full" {
		//optimise to full res, but at 85% qual webp
		part, err = writer.CreateFormFile("file", filepath.Base(filePath)+".webp")
	} else {
		//opt will be somehting like "720" or "1000", we've already trimmed the input
		part, err = writer.CreateFormFile("file", filepath.Base(filePath)+opt+".webp")
	}
	if err != nil {
		return nil, err
	}
	if opt == "" || opt == "none" {
		_, err = io.Copy(part, file)
		if err != nil {
			return nil, err
		}
	} else {
		optimised, err := img.OptimiseImage(filePath, opt)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(part, optimised)
		if err != nil {
			return nil, err
		}
	}
	writer.Close()
	req, err := http.NewRequest("POST", "https://cdn.hackclub.com/api/v4/upload", body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var upload types.Upload
	if err := json.NewDecoder(resp.Body).Decode(&upload); err != nil {
		return nil, err
	}
	// fmt.Printf("upload object: %+v\n", upload)
	upload.FileLoc = filePath

	return &upload, nil
}

func init() {
	rootCmd.AddCommand(upCmd)
	upCmd.Flags().BoolP("recursive", "r", false, "Upload directories recursively")
	upCmd.Flags().StringP("optimise", "o", "", "Optimise to certain resolutions. Comma seperated list of max width/heights (whichever is larger will be used). For example: 1000,2000")
	upCmd.Flags().BoolP("anon", "a", false, "Won't save to the db. Note: deleting files won't work")
}
