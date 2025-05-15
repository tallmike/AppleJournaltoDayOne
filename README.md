# AppleJournaltoDayOne
Convert from Apple Journal to Day One App JSON zip file.

Go program to convert Apple Journal File entries to Day One App JSON zip file format.

To Use (assuming go is installed on your machine).

Create a new directory (e.g. journalconverter) and cd there.
Download main.go and go.mod code and initialize
  go mod init journalconverter
Get the dependencies :
  go get github.com/JohannesKaufmann/html-to-markdown@v1.6.0
  go get github.com/PuerkitoBio/goquery@v1.9.2
  go get github.com/google/uuid@v1.6.0
Build
  go build
Run
  ./journalconverter -i /path/to/your/AppleJournalEntries.zip -o ./ConvertedDayOne.zip -tz America/Los_Angeles

Known Limitations
 : disguards location data
