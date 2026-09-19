package appleappidentity

import "strings"

// SourceURL identifies Apple's iPhone and iPad app catalogue. ReviewedOn records
// when the bundled snapshot was checked against that source, not an OS version.
const (
	SourceURL  = "https://support.apple.com/en-euro/guide/deployment/depece748c41/web"
	ReviewedOn = "2026-09-19"
)

// App identifies an Apple app listed in the iPhone and iPad catalogue. The source
// includes preinstalled and downloadable apps without per-device availability.
type App struct {
	Name     string `json:"name"`
	BundleID string `json:"bundleID"`
}

// Search returns catalogue entries whose name or bundle ID contains term,
// ignoring case and surrounding whitespace. An empty term returns all entries
// in source order. The returned slice is independent of the bundled catalogue.
// No network request or device inspection is performed.
func Search(term string) []App {
	term = strings.ToLower(strings.TrimSpace(term))
	matches := make([]App, 0)
	for _, app := range catalog {
		if strings.Contains(strings.ToLower(app.Name), term) || strings.Contains(strings.ToLower(app.BundleID), term) {
			matches = append(matches, app)
		}
	}
	return matches
}

// Lookup returns the entry with the exact, case-sensitive bundle ID. The boolean
// is false when the snapshot has no matching entry; absence does not establish
// that an app does not exist.
func Lookup(bundleID string) (App, bool) {
	for _, app := range catalog {
		if app.BundleID == bundleID {
			return app, true
		}
	}
	return App{}, false
}

// Names and identifiers are facts from SourceURL, reviewed on ReviewedOn.
// Preserve Apple's spelling and case. This is a source snapshot, not a mapping
// of OS releases to installed applications. Update its provenance when reviewed.
var catalog = [...]App{
	{Name: "App Store", BundleID: "com.apple.AppStore"},
	{Name: "App Store Connect", BundleID: "com.apple.AppStoreConnect"},
	{Name: "Apple Business", BundleID: "com.apple.business-essentials"},
	{Name: "Apple Configurator", BundleID: "com.apple.ios.configurator"},
	{Name: "Apple Store", BundleID: "com.apple.store.Jolly"},
	{Name: "Apple Vision Pro", BundleID: "com.apple.visionproapp"},
	{Name: "Barcode Scanner", BundleID: "com.apple.BarcodeScanner"},
	{Name: "Books", BundleID: "com.apple.iBooks"},
	{Name: "Calculator", BundleID: "com.apple.calculator"},
	{Name: "Calendar", BundleID: "com.apple.mobilecal"},
	{Name: "Camera", BundleID: "com.apple.camera"},
	{Name: "Classical", BundleID: "com.apple.music.classical"},
	{Name: "Classroom", BundleID: "com.apple.classroom"},
	{Name: "Clips", BundleID: "com.apple.clips"},
	{Name: "Clock", BundleID: "com.apple.mobiletimer"},
	{Name: "Compass", BundleID: "com.apple.compass"},
	{Name: "Contacts", BundleID: "com.apple.MobileAddressBook"},
	{Name: "Developer", BundleID: "developer.apple.wwdc-Release"},
	{Name: "FaceTime", BundleID: "com.apple.facetime"},
	{Name: "Files", BundleID: "com.apple.DocumentsApp"},
	{Name: "Final Cut Camera", BundleID: "com.apple.FinalCutApp.companion"},
	{Name: "Final Cut Pro", BundleID: "com.apple.FinalCutApp"},
	{Name: "Find My", BundleID: "com.apple.findmy"},
	{Name: "Fitness", BundleID: "com.apple.Fitness"},
	{Name: "Freeform", BundleID: "com.apple.freeform"},
	{Name: "Games", BundleID: "com.apple.games"},
	{Name: "GarageBand", BundleID: "com.apple.mobilegarageband"},
	{Name: "Health", BundleID: "com.apple.Health"},
	{Name: "Home", BundleID: "com.apple.Home"},
	{Name: "iCloud Drive", BundleID: "com.apple.iCloudDriveApp"},
	{Name: "iMovie", BundleID: "com.apple.iMovie"},
	{Name: "Invites", BundleID: "com.apple.rsvp"},
	{Name: "iTunes Store", BundleID: "com.apple.MobileStore"},
	{Name: "Journal", BundleID: "com.apple.journal"},
	{Name: "Keynote", BundleID: "com.apple.Keynote"},
	{Name: "Logic Pro", BundleID: "com.apple.mobilelogic"},
	{Name: "Logic Remote", BundleID: "com.apple.musicapps.remote"},
	{Name: "Magnifier", BundleID: "com.apple.Magnifier"},
	{Name: "Mail", BundleID: "com.apple.mobilemail"},
	{Name: "Maps", BundleID: "com.apple.Maps"},
	{Name: "Measure", BundleID: "com.apple.measure"},
	{Name: "Messages", BundleID: "com.apple.MobileSMS"},
	{Name: "Music", BundleID: "com.apple.Music"},
	{Name: "News", BundleID: "com.apple.news"},
	{Name: "Notes", BundleID: "com.apple.mobilenotes"},
	{Name: "Numbers", BundleID: "com.apple.Numbers"},
	{Name: "Pages", BundleID: "com.apple.Pages"},
	{Name: "Passwords", BundleID: "com.apple.Passwords"},
	{Name: "Phone", BundleID: "com.apple.mobilephone"},
	{Name: "Photo Booth", BundleID: "com.apple.Photo-Booth"},
	{Name: "Photomator", BundleID: "com.pixelmatorteam.pixelmator.touch.x.photo"},
	{Name: "Photos", BundleID: "com.apple.mobileslideshow"},
	{Name: "Pixelmator Classic iOS", BundleID: "com.pixelmatorteam.pixelmator.touch"},
	{Name: "Playground", BundleID: "com.apple.GenerativePlaygroundApp"},
	{Name: "Podcasts", BundleID: "com.apple.podcasts"},
	{Name: "Preview", BundleID: "com.apple.Preview"},
	{Name: "Reality Composer", BundleID: "com.apple.RealityComposer"},
	{Name: "Reminders", BundleID: "com.apple.reminders"},
	{Name: "Research", BundleID: "com.apple.Research"},
	{Name: "Safari", BundleID: "com.apple.mobilesafari"},
	{Name: "Schoolwork", BundleID: "com.apple.schoolwork.ClassKitApp"},
	{Name: "Settings", BundleID: "com.apple.Preferences"},
	{Name: "Shazam", BundleID: "com.shazam.Shazam"},
	{Name: "Shortcuts", BundleID: "com.apple.shortcuts"},
	{Name: "Sports", BundleID: "com.apple.sports"},
	{Name: "Stocks", BundleID: "com.apple.stocks"},
	{Name: "Swift Playground", BundleID: "com.apple.Playgrounds"},
	{Name: "TestFlight", BundleID: "com.apple.TestFlight"},
	{Name: "Tips", BundleID: "com.apple.tips"},
	{Name: "Translate", BundleID: "com.apple.Translate"},
	{Name: "TV", BundleID: "com.apple.tv"},
	{Name: "Voice Memos", BundleID: "com.apple.VoiceMemos"},
	{Name: "Wallet", BundleID: "com.apple.Passbook"},
	{Name: "Watch", BundleID: "com.apple.Bridge"},
	{Name: "Weather", BundleID: "com.apple.weather"},
}
