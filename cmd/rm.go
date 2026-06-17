package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/louis-bourgault/hccdn-cli/db"
	"github.com/louis-bourgault/hccdn-cli/types"
	"github.com/spf13/cobra"
)

// rmCmd represents the rm command
var rmCmd = &cobra.Command{
	Use:   "rm",
	Short: "Delete things from the cdn",
	Long:  `Delete uploads. Can be filename, direcory (wip), session (wip), or "all`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("rm called")
		if len(args) == 1 {
			uploadsToDelete := []types.Upload{}
			var err error
			location := args[0]

			session, err, sessionExists := db.GetSessionById(location)

			if location == "all" {
				//get rid of everything in the db
				uploadsToDelete, err = db.GetAllUploads()

			} else if sessionExists {
				fmt.Printf("the session exists \n")
				uploadsToDelete, err = db.GetUploadsBySession(session.Id)
			} else {
				fp, err := filepath.Abs(filepath.Clean(location))
				if err != nil {
					fmt.Printf("error getting absolute path: %s\n", err)
					return
				}
				info, err := os.Stat(fp)
				if err != nil {
					fmt.Printf("error stating file: %s\n", err)
					return
				}
				if info.IsDir() {
					// fmt.Printf("killigg all the children in %s\n", fp)

					childFiles, err := db.GetChildFiles(fp)

					if err != nil {
						fmt.Printf("error getting child files: %s\n", err)
						return
					}
					// fmt.Printf("child files: %+v\n", childFiles)
					for _, f := range childFiles {
						fileUps, err := db.GetUploadsByFilename(f)
						// fmt.Printf("got uploads for child file %s: %+v\n", f, fileUps)
						if err != nil {
							fmt.Printf("error getting uploads for child file %s: %s\n", f, err)
							return
						}
						uploadsToDelete = append(uploadsToDelete, fileUps...)
					}

				} else {

					uploadsToDelete, err = db.GetUploadsByFilename(info.Name())
				}
			}
			if err != nil {
				fmt.Printf("error getting uploads to delete: %s\n", err)
				return
			}
			for _, upload := range uploadsToDelete {
				// fmt.Printf("deleting upload: %+v\n", upload)
				err = DeleteFromCDN(upload)
				if err != nil {
					fmt.Printf("error deleting upload %s: %s\n", upload.Filename, err)
				} else {
					fmt.Printf("deleted upload %s\n", upload.Filename)
					db.DeleteUpload(upload.Id)
				}
			}
			if sessionExists {
				err = db.DeleteSession(session.Id)
				if err != nil {
					fmt.Printf("error deleting session %s: %s\n", session.Id, err)
				} else {
					fmt.Printf("deleted session %s\n", session.Id)
				}
			}
		}
	},
}

func DeleteFromCDN(upload types.Upload) error {

	//from docs:
	// curl -X DELETE \
	// -H "Authorization: Bearer sk_cdn_your_key_here" \
	// https://cdn.hackclub.com/api/v4/upload/01234567-89ab-cdef-0123-456789abcdef
	id := upload.Id
	if id == "" {
		return fmt.Errorf("upload id is empty")
	}
	// fmt.Printf("deleting upload with id %s\n", id)
	url := fmt.Sprintf("https://cdn.hackclub.com/api/v4/upload/%s", upload.Id)
	// fmt.Printf("sending delete request to %s", url)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", os.Getenv("HCCDN_API_KEY")))
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// fmt.Printf("response status: %d\n", resp.StatusCode)
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete upload: %s", string(bodyBytes))
	}
	return nil
}

func init() {
	rootCmd.AddCommand(rmCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// rmCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// rmCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
