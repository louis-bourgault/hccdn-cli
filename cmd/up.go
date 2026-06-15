package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/louis-bourgault/hccdn-cli/db"
	"github.com/spf13/cobra"
)

// upCmd represents the up command
var upCmd = &cobra.Command{
	Use:   "up",
	Short: "upload file or directory",
	Long:  `You can upload files or directories. You can also use the -r flag to upload directories recursively.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("up called")
		if len(args) == 1 {
			location := args[0]
			fmt.Printf("uploading %s\n", location)
			location, err := filepath.Abs(filepath.Clean(location))
			if err != nil {
				return err
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
			//upload the things
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(upCmd)
	upCmd.Flags().BoolP("recursive", "r", false, "Upload directories recursively")
	upCmd.Flags().StringP("optimise", "o", "", "Optimise to certain resolutions. Comma seperated list of max width/heights (whichever is larger will be used). For example: 1000,2000")
	upCmd.Flags().BoolP("anon", "a", false, "Won't save to the db. Note: deleting files won't work")
}
