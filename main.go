package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const (
	serviceAccountFile = "service_account.json"
	backupFolderID     = ""
	pgUser             = "postgres"
	pgPassword         = ""
	pgDatabase         = ""
	pgHost             = "localhost"
	pgPort             = "5432"
	containerName      = ""
)

func main() {
	// Generate the backup file
	backupFile := fmt.Sprintf("backup_%s.sql", time.Now().Format("20060102_150405"))
	err := backupPostgreSQL(backupFile)
	if err != nil {
		log.Fatalf("Failed to create backup: %v", err)
	}

	// Upload to Google Drive
	err = uploadToDrive(backupFile)
	if err != nil {
		log.Fatalf("Failed to upload to Google Drive: %v", err)
	}

	// Clean up local backup file
	err = os.Remove(backupFile)
	if err != nil {
		log.Printf("Warning: Failed to clean up local backup file: %v", err)
	} else {
		log.Printf("Successfully cleaned up local backup file: %s", backupFile)
	}

	// Delete old backups from Google Drive
	err = deleteOldBackups()
	if err != nil {
		log.Printf("Warning: Failed to delete old backups: %v", err)
	}

	fmt.Println("Backup and upload successful!")
}

// backupPostgreSQL runs pg_dump and compresses the output
func backupPostgreSQL(filename string) error {
	cmd := exec.Command("docker", "exec", containerName, "pg_dump",
		"-U", pgUser, "-F", "c", pgDatabase)

	// Create output file
	outFile, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer outFile.Close()

	cmd.Stdout = outFile
	return cmd.Run()
}

// uploadToDrive uploads a file to Google Drive
func uploadToDrive(filename string) error {
	ctx := context.Background()
	b, err := os.ReadFile(serviceAccountFile)
	if err != nil {
		return fmt.Errorf("failed to read service account file: %w", err)
	}

	config, err := google.CredentialsFromJSON(ctx, b, drive.DriveFileScope)
	if err != nil {
		return fmt.Errorf("failed to create credentials from JSON: %w", err)
	}

	srv, err := drive.NewService(ctx, option.WithCredentials(config))
	if err != nil {
		return fmt.Errorf("failed to create Drive client: %w", err)
	}

	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Prepare file upload
	f := &drive.File{
		Name:    filename,
		Parents: []string{backupFolderID},
	}

	// Upload the file
	_, err = srv.Files.Create(f).Media(file).Do()
	if err != nil {
		return fmt.Errorf("failed to upload file to Drive: %w", err)
	}

	fmt.Println("File uploaded successfully:", filename)
	return nil
}

// deleteOldBackups removes files older than 30 days from Google Drive
func deleteOldBackups() error {
	ctx := context.Background()
	b, err := os.ReadFile(serviceAccountFile)
	if err != nil {
		return fmt.Errorf("failed to read service account file: %w", err)
	}

	config, err := google.CredentialsFromJSON(ctx, b, drive.DriveFileScope)
	if err != nil {
		return fmt.Errorf("failed to create credentials from JSON: %w", err)
	}

	srv, err := drive.NewService(ctx, option.WithCredentials(config))
	if err != nil {
		return fmt.Errorf("failed to create Drive client: %w", err)
	}

	// Get files from the backup folder
	query := fmt.Sprintf("'%s' in parents", backupFolderID)
	fileList, err := srv.Files.List().Q(query).Fields("files(id, name, createdTime)").Do()
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	now := time.Now()
	deletedCount := 0

	for _, file := range fileList.Files {
		createdTime, err := time.Parse(time.RFC3339, file.CreatedTime)
		if err != nil {
			log.Printf("Skipping file %s: invalid date format\n", file.Name)
			continue
		}

		// If the file is older than 30 days, delete it
		if now.Sub(createdTime).Hours() > 30*24 {
			err = srv.Files.Delete(file.Id).Do()
			if err != nil {
				log.Printf("Failed to delete %s: %v\n", file.Name, err)
			} else {
				fmt.Printf("Deleted old backup: %s\n", file.Name)
				deletedCount++
			}
		}
	}

	fmt.Printf("Cleanup complete. Deleted %d old backups.\n", deletedCount)
	return nil
}
