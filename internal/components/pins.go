package components

// The pinned downloads `facet install` fetches, by component and platform
// (GOOS/GOARCH). Each one is checked against its SHA-256 before anything is
// unpacked. Change a version here together with every one of its digests.
//
// Sources: Node.js from nodejs.org (digests from its SHASUMS256.txt); FFmpeg
// for Windows from Gyan's builds (GitHub release digest; x64, which Windows
// on Arm runs too) and for macOS and Linux from Martin Riedl's release builds
// (their .sha256 files); the browser from Chrome for Testing, and for Linux
// arm64, which Chrome for Testing does not build, Remotion's own headless
// shell (digests taken from the downloads); the Piper voice from rhasspy's
// piper-voices at v1.0.0.

const (
	nodeVersion      = "24.18.0"
	nodeMinimumMajor = 22
	ffmpegVersion    = "9.0.2"
	browserVersion   = "152.0.7977.30"
	piperVersion     = "1.8.0"
	piperVoice       = "en_US-lessac-medium"
)

// A download is one pinned archive and what to take from it.
type download struct {
	// Name is the file's name in a download folder (`facet install --from`);
	// unique across every pin.
	Name   string
	URL    string
	SHA256 string
	Size   int64
	// Strip drops this many leading folders from every archive path.
	Strip int
	// Files, when set, takes only these archive paths (after Strip), each to
	// the path it maps to. Unset takes everything as it is.
	Files map[string]string
	// Raw places the downloaded file itself at this path, unpacking nothing.
	Raw string
}

func nodeDownload(platform, ext, sha string, size int64) download {
	stem := "node-v" + nodeVersion + "-" + platform
	return download{Name: stem + ext, URL: "https://nodejs.org/dist/v" + nodeVersion + "/" + stem + ext, SHA256: sha, Size: size, Strip: 1}
}

var nodePins = map[string][]download{
	"windows/amd64": {nodeDownload("win-x64", ".zip", "0ae68406b42d7725661da979b1403ec9926da205c6770827f33aac9d8f26e821", 0)},
	"windows/arm64": {nodeDownload("win-arm64", ".zip", "f274669adb93b1fd0fbf8f21fd078609e9dcc84333d4f2718d2dde3f9a161a01", 0)},
	"linux/amd64":   {nodeDownload("linux-x64", ".tar.gz", "783130984963db7ba9cbd01089eaf2c2efb055c7c1693c943174b967b3050cb8", 0)},
	"linux/arm64":   {nodeDownload("linux-arm64", ".tar.gz", "6b4484c2190274175df9aa8f28e2d758a819cb1c1fe6ab481e2f95b463ab8508", 0)},
	"darwin/amd64":  {nodeDownload("darwin-x64", ".tar.gz", "dfd0dbd3e721503434df7b7205e719f61b3a3a31b2bcf9729b8b91fea240f080", 0)},
	"darwin/arm64":  {nodeDownload("darwin-arm64", ".tar.gz", "e1a97e14c99c803e96c7339403282ea05a499c32f8d83defe9ef5ec66f979ed1", 0)},
}

var gyanFFmpeg = download{
	Name:   "ffmpeg-" + ffmpegVersion + "-essentials_build.zip",
	URL:    "https://github.com/GyanD/codexffmpeg/releases/download/" + ffmpegVersion + "/ffmpeg-" + ffmpegVersion + "-essentials_build.zip",
	SHA256: "60f467265b1e312373dbcd92200c2618a74850f98d3d078e94296bb3fa2047ba",
	Size:   114768076,
	Strip:  1,
	Files:  map[string]string{"bin/ffmpeg.exe": "bin/ffmpeg.exe", "bin/ffprobe.exe": "bin/ffprobe.exe"},
}

// riedl is one of Martin Riedl's single-program zips for os/arch.
func riedl(os, arch, build, program, sha string) download {
	return download{
		Name:   program + "-" + ffmpegVersion + "-" + os + "-" + arch + ".zip",
		URL:    "https://ffmpeg.martin-riedl.de/download/" + os + "/" + arch + "/" + build + "_" + ffmpegVersion + "/" + program + ".zip",
		SHA256: sha,
		Files:  map[string]string{program: "bin/" + program},
	}
}

var ffmpegPins = map[string][]download{
	"windows/amd64": {gyanFFmpeg},
	"windows/arm64": {gyanFFmpeg},
	"darwin/amd64": {
		riedl("macos", "amd64", "1789931006", "ffmpeg", "7c6b4125b191cbf773832dc51f424cf2b6bb7da43007d1e066f95909e47cacd4"),
		riedl("macos", "amd64", "1789931006", "ffprobe", "2322438ed2f6319a691291b247d09c69dcaa3a982460d1f269a7e1af335cfdfd"),
	},
	"darwin/arm64": {
		riedl("macos", "arm64", "1789931890", "ffmpeg", "c8ed4c4e6978a03c485edbfe4e0a5dc2380f8a30bba5150531b31b094492d924"),
		riedl("macos", "arm64", "1789931890", "ffprobe", "fcbe839537485eaee7a7a8bc5cbc0f90d53617e80943e8a5b2e31cb851197ea6"),
	},
	"linux/amd64": {
		riedl("linux", "amd64", "1789931100", "ffmpeg", "fa8ecf4abbd290d98f7d188b8649cc6b391ae209a98452be955a15aab1909d7f"),
		riedl("linux", "amd64", "1789931100", "ffprobe", "3f428c49070be3d24ec338602b76d412e401ffcb8a5641ef0e729181a232fc32"),
	},
	"linux/arm64": {
		riedl("linux", "arm64", "1789931697", "ffmpeg", "93a76ae90db5474eecdf951a729857c64f3de23567228d6a7d5e6e8e3cd1021b"),
		riedl("linux", "arm64", "1789931697", "ffprobe", "bcbe80fb741c180083327afaf5434812e006b33cacde2016b9aeaf6936128330"),
	},
}

func chromeForTesting(platform, sha string, size int64) download {
	name := "chrome-headless-shell-" + platform + ".zip"
	return download{
		Name:   "chrome-headless-shell-" + browserVersion + "-" + platform + ".zip",
		URL:    "https://storage.googleapis.com/chrome-for-testing-public/" + browserVersion + "/" + platform + "/" + name,
		SHA256: sha, Size: size, Strip: 1,
	}
}

var cftWindows = chromeForTesting("win64", "5d7df999a6e4a65a1b16b25b61064f7337b8aa8ee2ed1b4e07bfdd24f6e4275e", 119527247)

var browserPins = map[string][]download{
	"windows/amd64": {cftWindows},
	"windows/arm64": {cftWindows},
	"linux/amd64":   {chromeForTesting("linux64", "1b150320178ecabe39726bcf3198ebb896ba2b7a07d870db1710914c42d55221", 119388396)},
	"darwin/arm64":  {chromeForTesting("mac-arm64", "1db16c16e72bedfa747ed01c207a3fe8ee7370c35f2094279eb8878c996e8c79", 97532843)},
	"darwin/amd64":  {chromeForTesting("mac-x64", "48bf06f34412f9df5c7d3f3306d61a54a050789f395dd0129bae2234adb0033a", 102653759)},
	// Remotion's build of the same headless shell, at the version Remotion
	// tests; it needs glibc 2.35 or newer.
	"linux/arm64": {{
		Name:   "chromium-headless-shell-linux-arm64-149.0.7790.0.zip",
		URL:    "https://remotion.media/chromium-headless-shell-linux-arm64-149.0.7790.0.zip",
		SHA256: "c8ac2ea8fed57653d07c80caacb67bb0e833af32012059bfccf90b6246302706", Size: 92243209, Strip: 1,
	}},
}

// browserVersions names the browser each platform gets, when it differs.
var browserVersions = map[string]string{"linux/arm64": "149.0.7790.0"}

const voiceBase = "https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/en/en_US/lessac/medium/"

var voicePins = []download{
	{Name: piperVoice + ".onnx", URL: voiceBase + piperVoice + ".onnx", SHA256: "5efe09e69902187827af646e1a6e9d269dee769f9877d17b16b1b46eeaaf019f", Size: 63201294, Raw: piperVoice + ".onnx"},
	{Name: piperVoice + ".onnx.json", URL: voiceBase + piperVoice + ".onnx.json", SHA256: "efe19c417bed055f2d69908248c6ba650fa135bc868b0e6abb3da181dab690a0", Size: 4885, Raw: piperVoice + ".onnx.json"},
}
