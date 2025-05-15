package main

import (
	"archive/zip"
	"crypto/md5"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
)

// --- Day One Data Structures ---
type DayOnePhoto struct {
	MD5          string `json:"md5"`
	Type         string `json:"type"`
	Identifier   string `json:"identifier"`
	CreationDate string `json:"creationDate"` // ISO 8601
	// Width        int    `json:"width,omitempty"` // Not implementing for simplicity
	// Height       int    `json:"height,omitempty"`// Not implementing for simplicity
}

type DayOneEntry struct {
	UUID         string        `json:"uuid"`
	CreationDate string        `json:"creationDate"` // ISO 8601
	ModifiedDate string        `json:"modifiedDate"` // ISO 8601
	Text         string        `json:"text"`
	Starred      bool          `json:"starred"`
	TimeZone     string        `json:"timeZone"`
	Photos       []DayOnePhoto `json:"photos,omitempty"`
	// Location (omitted as per user request)
	// Tags (omitted, not found in sample)
}

type DayOneJournal struct {
	Metadata map[string]string `json:"metadata"`
	Entries  []DayOneEntry     `json:"entries"`
}

// --- Global Markdown Converter ---
var markdownConverter *md.Converter

func init() {
	markdownConverter = md.NewConverter("", true, nil)
}

// --- Helper Functions ---

func newDayOneUUID() string {
	return strings.ReplaceAll(strings.ToUpper(uuid.New().String()), "-", "")
}

func calculateMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	// Ensure destination directory exists
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}


	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		// Check for ZipSlip vulnerability
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("%s: illegal file path", fpath)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close() // Close outFile before returning error
			return err
		}

		_, err = io.Copy(outFile, rc)

		// Close files inside the loop
		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}

// parseAppleDate parses dates like "Wednesday, May 14, 2025" or "Tuesday, December 12, 2023"
func parseAppleDate(dateStr string) (time.Time, error) {
	// Normalize by removing the day of the week part
	parts := strings.SplitN(dateStr, ",", 2)
	if len(parts) == 2 {
		dateStr = strings.TrimSpace(parts[1]) // "May 14, 2025" or "December 12, 2023"
	}

	// Try parsing "January 2, 2006" format
	layouts := []string{
		"January 2, 2006", // For "May 14, 2025"
		"Jan 2, 2006",     // Just in case
	}
	var t time.Time
	var err error
	for _, layout := range layouts {
		t, err = time.Parse(layout, dateStr)
		if err == nil {
			// Set time to noon UTC for consistency, as Apple Journal HTML doesn't provide time
			t = time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, time.UTC)
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("failed to parse date string '%s' with known layouts: %w", dateStr, err)
}


func processEntryHTML(htmlFilePath string, baseResourcesPath string, defaultTimeZone string) (DayOneEntry, map[string]string, error) {
	file, err := os.Open(htmlFilePath)
	if err != nil {
		return DayOneEntry{}, nil, fmt.Errorf("opening HTML file %s: %w", htmlFilePath, err)
	}
	defer file.Close()

	doc, err := goquery.NewDocumentFromReader(file)
	if err != nil {
		return DayOneEntry{}, nil, fmt.Errorf("parsing HTML file %s: %w", htmlFilePath, err)
	}

	entry := DayOneEntry{
		UUID:    newDayOneUUID(),
		Starred: false, // Default
		TimeZone: defaultTimeZone,
		Photos:  make([]DayOnePhoto, 0),
	}
	mediaToCopy := make(map[string]string) // originalPath -> dayOneZipPath

	// --- Extract Date ---
	dateStr := strings.TrimSpace(doc.Find("div.pageHeader").First().Text())
	if dateStr == "" {
		log.Printf("Warning: No date found in pageHeader for %s. Skipping entry.", htmlFilePath)
		return DayOneEntry{}, nil, fmt.Errorf("no date found in pageHeader for %s", htmlFilePath)
	}
	creationTime, err := parseAppleDate(dateStr)
	if err != nil {
		log.Printf("Warning: Could not parse date '%s' for %s: %v. Skipping entry.", dateStr, htmlFilePath, err)
		return DayOneEntry{}, nil, fmt.Errorf("could not parse date '%s' for %s: %w",dateStr, htmlFilePath, err)
	}
	isoDate := creationTime.Format(time.RFC3339) // "2006-01-02T15:04:05Z07:00"
	entry.CreationDate = isoDate
	entry.ModifiedDate = isoDate // Default modified to creation

	// --- Extract Title ---
	var entryTitle string
	titleSelection := doc.Find("div.title span.s2").First() // As seen in 2025-05-14 sample
	if titleSelection.Length() > 0 {
		entryTitle = strings.TrimSpace(titleSelection.Text())
	} else {
		// Fallback to filename if it contains a title part
		fn := filepath.Base(htmlFilePath)
		fn = strings.TrimSuffix(fn, filepath.Ext(fn)) // Remove .html
		parts := strings.SplitN(fn, "_", 2) // YYYY-MM-DD_The_Title
		if len(parts) > 1 && strings.Contains(parts[0], "-") { // Check if first part looks like a date
			entryTitle = strings.ReplaceAll(parts[1], "_", " ")
		}
	}


	// --- Extract Body Content & Media ---
	var bodyMarkdownBuilder strings.Builder
	var currentPContent strings.Builder // To accumulate content of a paragraph before converting

	// Helper function to convert accumulated paragraph content
	convertAndAppendP := func() {
		if currentPContent.Len() > 0 {
			htmlFrag := currentPContent.String()
			// Remove wrapping <p> if the converter adds its own, or ensure structure is simple
			// For simple text, direct append might be fine after cleaning.
			// For complex <p> with spans, converter is better.
			markdownFrag, err := markdownConverter.ConvertString(htmlFrag)
			if err != nil {
				log.Printf("Warning: Markdown conversion error for a fragment in %s: %v", htmlFilePath, err)
			} else {
				bodyMarkdownBuilder.WriteString(strings.TrimSpace(markdownFrag) + "\n\n")
			}
			currentPContent.Reset()
		}
	}


	doc.Find("div.pageContainer").Children().Each(func(i int, s *goquery.Selection) {
		if s.Is("div.pageHeader") { // Already processed
			return
		}
		if s.Is("div.title") { // Already processed
			return
		}

		// Handle asset grid for photos
		if s.Is("div.assetGrid") {
			convertAndAppendP() // Convert any pending paragraph before the grid
			s.Find("div.gridItem.assetType_photo img.asset_image").Each(func(j int, imgSel *goquery.Selection) {
				imgSrc, exists := imgSel.Attr("src")
				if !exists || imgSrc == "" {
					return
				}

				// Path is relative from Entries/ folder, e.g., ../Resources/IMAGE_ID.png
				// So, join with the directory of the current HTML file, then evaluate.
				absImgSrc := filepath.Clean(filepath.Join(filepath.Dir(htmlFilePath), imgSrc))
				
				originalImageName := filepath.Base(absImgSrc)
				fileExt := strings.ToLower(filepath.Ext(originalImageName))
				if fileExt != ".png" && fileExt != ".jpg" && fileExt != ".jpeg" && fileExt != ".gif" {
					log.Printf("Warning: Skipping non-image media type '%s' from %s", fileExt, htmlFilePath)
					return
				}


				// Check if image exists (absImgSrc is now relative to the root of the extracted archive)
				if _, err := os.Stat(absImgSrc); os.IsNotExist(err) {
					log.Printf("Warning: Image file not found: %s (referenced in %s)", absImgSrc, htmlFilePath)
					return
				}


				photoUUID := newDayOneUUID()
				dayOnePhotoFilename := photoUUID + fileExt
				dayOnePhotoZipPath := filepath.Join("photos", dayOnePhotoFilename)

				md5Hash, err := calculateMD5(absImgSrc)
				if err != nil {
					log.Printf("Warning: Failed to calculate MD5 for %s: %v", absImgSrc, err)
					return
				}

				photo := DayOnePhoto{
					MD5:          md5Hash,
					Type:         strings.TrimPrefix(fileExt, "."),
					Identifier:   photoUUID,
					CreationDate: entry.CreationDate, // Use entry's creation date for photo
				}
				entry.Photos = append(entry.Photos, photo)
				mediaToCopy[absImgSrc] = dayOnePhotoZipPath // Map full path of original file to its new DayOne path

				bodyMarkdownBuilder.WriteString(fmt.Sprintf("![](dayone-moment://%s)\n\n", photoUUID))
			})
			return
		}

		// Handle body text paragraphs (p.p1, p.p2 as per samples, and div.bodyText itself)
		// The structure is a bit inconsistent:
		// 2023-12-12: <p class="p1"><span class="s1"><div class='bodyText'>...</div></span></p> <p class="p2">...</p>
		// 2025-05-14: <p class="p1"><span class="s1">...<div class='bodyText'></span></p><p class="p2">...</p>
		// We need to get the HTML content of these relevant text blocks.
		
		// Attempt to get outer HTML of the selection, then convert
		htmlContent, err := goquery.OuterHtml(s)
		if err != nil {
			log.Printf("Warning: Could not get HTML content for a section in %s: %v", htmlFilePath, err)
			return
		}
		// The HTML structure is simple enough that the markdown converter should handle it.
		// We are primarily interested in <p> tags within div.bodyText or at the same level as title/assetGrid.
		// Filter for <p> or <div class="bodyText">
		if s.Is("p") || s.Is("div.bodyText") || s.Parent().Is("div.bodyText") {
			 currentPContent.WriteString(htmlContent)
			 convertAndAppendP()
		} else if s.Find("div.bodyText").Length() > 0 { // If bodyText is a child
			s.Find("div.bodyText").Each(func(k int, bodyTextSel *goquery.Selection) {
				bodyHtml, _ := goquery.OuterHtml(bodyTextSel)
				currentPContent.WriteString(bodyHtml)
				convertAndAppendP()
			})
		} else if s.Find("p").Length() > 0 { // If <p> is a child
			s.Find("p").Each(func(k int, pSel *goquery.Selection) {
				pHtml, _ := goquery.OuterHtml(pSel)
				currentPContent.WriteString(pHtml)
				convertAndAppendP()
			})
		}
	})
	convertAndAppendP() // Convert any last paragraph

	entry.Text = strings.TrimSpace(bodyMarkdownBuilder.String())
	if entryTitle != "" {
		entry.Text = fmt.Sprintf("# %s\n\n%s", entryTitle, entry.Text)
	}


	if entry.Text == "" && len(entry.Photos) == 0 {
		log.Printf("Warning: Entry %s resulted in no text and no photos. Skipping.", htmlFilePath)
		return DayOneEntry{}, nil, fmt.Errorf("empty entry after processing %s", htmlFilePath)
	}


	return entry, mediaToCopy, nil
}


func createDayOneZip(outputZipPath string, journal DayOneJournal, mediaToCopy map[string]string, tempExtractBasePath string) error {
	zipFile, err := os.Create(outputZipPath)
	if err != nil {
		return fmt.Errorf("creating output zip %s: %w", outputZipPath, err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Add Journal.json
	jsonWriter, err := zipWriter.Create("Journal.json")
	if err != nil {
		return fmt.Errorf("creating Journal.json in zip: %w", err)
	}
	jsonData, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling journal data to JSON: %w", err)
	}
	if _, err := jsonWriter.Write(jsonData); err != nil {
		return fmt.Errorf("writing Journal.json to zip: %w", err)
	}

	// Add media files
	for originalPath, dayOneZipPath := range mediaToCopy {
		mediaWriter, err := zipWriter.Create(dayOneZipPath)
		if err != nil {
			log.Printf("Warning: Creating %s in zip: %v. Skipping this media file.", dayOneZipPath, err)
			continue
		}

		// originalPath is an absolute path to the file in the temp extraction directory
		mediaFile, err := os.Open(originalPath)
		if err != nil {
			log.Printf("Warning: Opening original media file %s: %v. Skipping this media file.", originalPath, err)
			continue
		}
		defer mediaFile.Close() // Close inside loop for each file

		if _, err := io.Copy(mediaWriter, mediaFile); err != nil {
			log.Printf("Warning: Copying media file %s to zip: %v. Skipping this media file.", originalPath, err)
			continue
		}
		log.Printf("Copied %s to %s in zip.", originalPath, dayOneZipPath)
	}

	return nil
}


func main() {
	inputZip := flag.String("i", "", "Input Apple Journal ZIP file path (required)")
	outputZip := flag.String("o", "", "Output Day One ZIP file path (required)")
	defaultTimeZone := flag.String("tz", "UTC", "Default Olson TimeZone for entries (e.g., America/New_York)")
	flag.Parse()

	if *inputZip == "" || *outputZip == "" {
		fmt.Println("Both input (-i) and output (-o) file paths are required.")
		flag.Usage()
		os.Exit(1)
	}

	log.Printf("Starting conversion from %s to %s", *inputZip, *outputZip)

	// 1. Create temp directory for extraction
	tempExtractDir, err := os.MkdirTemp("", "applejournal_extract_*")
	if err != nil {
		log.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() {
		log.Printf("Cleaning up temp directory: %s", tempExtractDir)
		if err := os.RemoveAll(tempExtractDir); err != nil {
			log.Printf("Warning: Failed to remove temp directory %s: %v", tempExtractDir, err)
		}
	}()
	log.Printf("Temporary extraction directory: %s", tempExtractDir)

	// 2. Unzip input Apple Journal zip
	log.Printf("Unzipping %s to %s...", *inputZip, tempExtractDir)
	if err := unzip(*inputZip, tempExtractDir); err != nil {
		log.Fatalf("Failed to unzip %s: %v", *inputZip, err)
	}
	log.Println("Unzip complete.")

	// 3. Determine base paths for Entries and Resources
	//    The samples imply a folder named "AppleJournalEntries" at the root of the zip.
	//    Let's check for that, or assume files are at the root of the temp dir.
	
	entriesPath := filepath.Join(tempExtractDir, "Entries")
	resourcesPath := filepath.Join(tempExtractDir, "Resources")

    // Check if the "AppleJournalEntries" folder exists after unzipping
    // If so, adjust entriesPath and resourcesPath
    potentialRootFolderName := ""
    filesInTemp, err := os.ReadDir(tempExtractDir)
    if err == nil && len(filesInTemp) == 1 && filesInTemp[0].IsDir() {
        // Common case: zip contains a single root folder
        potentialRootFolderName = filesInTemp[0].Name()
        testEntriesPath := filepath.Join(tempExtractDir, potentialRootFolderName, "Entries")
        if _, err := os.Stat(testEntriesPath); err == nil {
            entriesPath = testEntriesPath
            resourcesPath = filepath.Join(tempExtractDir, potentialRootFolderName, "Resources")
            log.Printf("Detected root folder '%s' in zip. Adjusted paths.", potentialRootFolderName)
        } else {
             log.Printf("Root folder '%s' detected, but 'Entries' subfolder not found within it. Assuming Entries/Resources are at the top level of the zip.", potentialRootFolderName)
			 entriesPath = filepath.Join(tempExtractDir, "Entries") // Fallback to direct subfolders
			 resourcesPath = filepath.Join(tempExtractDir, "Resources")
        }
    }


	if _, err := os.Stat(entriesPath); os.IsNotExist(err) {
		log.Fatalf("Entries folder not found at %s. Please ensure the zip structure is correct (e.g., ZipName/Entries/ or Entries/ at root).", entriesPath)
	}
	if _, err := os.Stat(resourcesPath); os.IsNotExist(err) {
		log.Printf("Warning: Resources folder not found at %s. Media linking might fail.", resourcesPath)
		// Continue if resources are optional, but log it.
	}


	dayOneJournal := DayOneJournal{
		Metadata: map[string]string{"version": "1.0"}, // As per Day One example
		Entries:  make([]DayOneEntry, 0),
	}
	// mediaToCopy stores original full path -> new DayOne zip path for all media across all entries
	allMediaToCopy := make(map[string]string)

	log.Printf("Processing HTML entries from: %s", entriesPath)
	err = filepath.WalkDir(entriesPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			log.Printf("Error accessing path %s: %v. Skipping.", path, walkErr)
			return walkErr // Propagate error to stop walking if critical
		}
		if d.IsDir() {
			return nil // Skip directories
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".html") || strings.HasSuffix(strings.ToLower(d.Name()), ".htm") {
			log.Printf("Processing entry: %s", path)
			entry, entryMedia, procErr := processEntryHTML(path, resourcesPath, *defaultTimeZone)
			if procErr != nil {
				log.Printf("Error processing entry %s: %v. Entry skipped.", path, procErr)
				return nil // Continue with next file even if one fails
			}
			// Check if entry is truly empty (e.g. only a date was found but no body/title)
			if entry.Text == "" && len(entry.Photos) == 0 {
				log.Printf("Skipping entry %s as it's empty after processing.", path)
			} else {
				dayOneJournal.Entries = append(dayOneJournal.Entries, entry)
				for original, dayOnePath := range entryMedia {
					allMediaToCopy[original] = dayOnePath
				}
			}
		}
		return nil
	})

	if err != nil {
		log.Fatalf("Error walking through entries directory %s: %v", entriesPath, err)
	}

	if len(dayOneJournal.Entries) == 0 {
		log.Println("No journal entries were successfully processed. Output will be empty.")
	} else {
		log.Printf("Processed %d entries.", len(dayOneJournal.Entries))
	}


	// 5. Create output Day One Zip
	log.Printf("Creating Day One zip file: %s", *outputZip)
	if err := createDayOneZip(*outputZip, dayOneJournal, allMediaToCopy, tempExtractDir); err != nil {
		log.Fatalf("Failed to create Day One zip: %v", err)
	}

	log.Println("Conversion complete!")
	log.Printf("Output written to: %s", *outputZip)
}
