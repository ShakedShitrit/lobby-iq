package rlsetup

import (
	"strings"
	"testing"
)

// realUserIni is a verbatim copy of a TAStatsAPI.ini written by Rocket League
// itself, CRLF and all. Fixtures invented by hand would agree with whatever
// the parser happens to do; this one agrees with the game.
const realUserIni = "[TAGame.MatchStatsExporter_TA]\r\n" +
	"Port=49123\r\n" +
	"WebPort=49124\r\n" +
	"PacketSendRate=2\r\n" +
	"\r\n" +
	"[IniVersion]\r\n" +
	"0=1787076161.000000\r\n"

// realDefaultIni is the game's own DefaultStatsAPI.ini, which is what a
// regenerated user file is built from - comments included.
const realDefaultIni = "[TAGame.MatchStatsExporter_TA]\r\n" +
	"\r\n" +
	"; Port the client will listen for tcp connections on (must be different than WebPort, set to 0 to disable)\r\n" +
	"Port=49123\r\n" +
	"\r\n" +
	"; Port the client will listen for web connections on (must be different than Port, set to 0 to disable)\r\n" +
	"WebPort=49124\r\n" +
	"\r\n" +
	"; How many times per second the game sends the update state (capped at 120, 0 disables this feature)\r\n" +
	"PacketSendRate=2\r\n"

// The common case by far: the game already ships these defaults, so a correct
// installer must be able to tell "nothing to do" from "needs fixing" and leave
// the file untouched.
func TestAlreadyCorrectIsLeftAlone(t *testing.T) {
	got, changes := patchStatsAPI(realUserIni, 49123)
	if len(changes) != 0 {
		t.Errorf("wanted no changes, got %v", changes)
	}
	if got != realUserIni {
		t.Errorf("file was rewritten despite no changes:\n%q", got)
	}
}

func TestDefaultIniNeedsNothingEither(t *testing.T) {
	got, changes := patchStatsAPI(realDefaultIni, 49123)
	if len(changes) != 0 {
		t.Errorf("wanted no changes, got %v", changes)
	}
	if got != realDefaultIni {
		t.Error("the game's own default file should survive untouched")
	}
}

// Everything the game wrote apart from the values we correct has to survive -
// WebPort is a real setting and [IniVersion] is what stops Rocket League
// regenerating the file.
func TestUnrelatedSettingsSurvive(t *testing.T) {
	got, changes := patchStatsAPI(realUserIni, 50000)
	if len(changes) != 1 || changes[0].Key != keyPort {
		t.Fatalf("wanted one Port change, got %v", changes)
	}
	for _, want := range []string{"WebPort=49124", "[IniVersion]", "0=1787076161.000000", "Port=50000"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from result:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Port=49123") {
		t.Error("the old port is still in the file")
	}
}

// PacketSendRate=0 is the one way this file can look configured and still
// leave LobbyIQ with nothing to read.
func TestZeroPacketSendRateIsRepaired(t *testing.T) {
	in := "[TAGame.MatchStatsExporter_TA]\r\nPort=49123\r\nPacketSendRate=0\r\n"
	got, changes := patchStatsAPI(in, 49123)
	if len(changes) != 1 || changes[0].Key != keyPacketSendRate {
		t.Fatalf("wanted PacketSendRate repaired, got %v", changes)
	}
	if !strings.Contains(got, "PacketSendRate=2") {
		t.Errorf("rate not repaired:\n%s", got)
	}
}

// A rate someone raised on purpose is theirs, not ours to normalise.
func TestHigherPacketSendRateIsKept(t *testing.T) {
	in := "[TAGame.MatchStatsExporter_TA]\r\nPort=49123\r\nPacketSendRate=30\r\n"
	got, changes := patchStatsAPI(in, 49123)
	if len(changes) != 0 {
		t.Errorf("wanted no changes, got %v", changes)
	}
	if !strings.Contains(got, "PacketSendRate=30") {
		t.Error("a deliberately raised rate was overwritten")
	}
}

func TestMissingFileGetsAWholeSection(t *testing.T) {
	got, changes := patchStatsAPI("", 49123)
	if len(changes) != 2 {
		t.Fatalf("wanted Port and PacketSendRate added, got %v", changes)
	}
	for _, want := range []string{"[" + StatsAPISection + "]", "Port=49123", "PacketSendRate=2"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from result:\n%s", want, got)
		}
	}
}

// The keys must land inside our section. Appended to the end of the file they
// would fall into whichever section came last and do nothing at all.
func TestKeysAreAddedInsideTheSection(t *testing.T) {
	in := "[TAGame.MatchStatsExporter_TA]\r\nWebPort=49124\r\n\r\n[IniVersion]\r\n0=123.000000\r\n"
	got, _ := patchStatsAPI(in, 49123)

	portAt := strings.Index(got, "Port=49123")
	versionAt := strings.Index(got, "[IniVersion]")
	if portAt < 0 {
		t.Fatalf("Port was never added:\n%s", got)
	}
	if portAt > versionAt {
		t.Errorf("Port landed in [IniVersion] instead of the exporter section:\n%s", got)
	}
}

// A file whose only section is something else must gain ours rather than have
// its settings quietly adopted.
func TestOtherSectionIsNotHijacked(t *testing.T) {
	in := "[IniVersion]\r\n0=123.000000\r\n"
	got, changes := patchStatsAPI(in, 49123)
	if len(changes) != 2 {
		t.Fatalf("wanted both keys added, got %v", changes)
	}
	if !strings.Contains(got, "["+StatsAPISection+"]") {
		t.Errorf("section was never added:\n%s", got)
	}
	if !strings.Contains(got, "[IniVersion]\r\n0=123.000000") {
		t.Errorf("the existing section was disturbed:\n%s", got)
	}
}

// Rocket League writes CRLF, and a file that comes back LF-only would show as
// entirely rewritten to anything comparing it.
func TestLineEndingsArePreserved(t *testing.T) {
	got, _ := patchStatsAPI(realUserIni, 50000)
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Error("result contains a bare LF; the file was CRLF")
	}

	lf := "[TAGame.MatchStatsExporter_TA]\nPort=1\n"
	gotLF, _ := patchStatsAPI(lf, 49123)
	if strings.Contains(gotLF, "\r\n") {
		t.Error("an LF file came back with CRLF")
	}
}

// Section and key names are matched case-insensitively because they come from
// a file a user may have hand-edited; a case difference must not cause a
// duplicate key to be appended.
func TestCaseInsensitiveMatching(t *testing.T) {
	in := "[tagame.matchstatsexporter_ta]\r\nport=49123\r\npacketsendrate=2\r\n"
	_, changes := patchStatsAPI(in, 49123)
	if len(changes) != 0 {
		t.Errorf("wanted no changes for a differently cased file, got %v", changes)
	}
}

// A duplicate key leaves the game reading one value while a naive check reads
// the other, so every occurrence has to be corrected.
func TestDuplicateKeysAreAllRewritten(t *testing.T) {
	in := "[TAGame.MatchStatsExporter_TA]\r\nPort=1\r\nPort=2\r\nPacketSendRate=2\r\n"
	got, _ := patchStatsAPI(in, 49123)
	if strings.Contains(got, "Port=1") || strings.Contains(got, "Port=2") {
		t.Errorf("a stale port survived:\n%s", got)
	}
}

// Commented-out settings are documentation, not configuration - treating one
// as a real key would leave the actual setting unwritten.
func TestCommentsAreNotSettings(t *testing.T) {
	in := "[TAGame.MatchStatsExporter_TA]\r\n; Port=9999\r\nPacketSendRate=2\r\n"
	got, changes := patchStatsAPI(in, 49123)
	if len(changes) != 1 || changes[0].Key != keyPort {
		t.Fatalf("wanted Port added, got %v", changes)
	}
	if !strings.Contains(got, "; Port=9999") {
		t.Error("the comment was eaten")
	}
	if !strings.Contains(got, "\r\nPort=49123") {
		t.Errorf("Port was not actually set:\n%s", got)
	}
}

// Running twice must not keep changing the file, or the "did anything change"
// signal the caller reports to the user is meaningless.
func TestPatchIsIdempotent(t *testing.T) {
	once, _ := patchStatsAPI(realUserIni, 50000)
	twice, changes := patchStatsAPI(once, 50000)
	if len(changes) != 0 {
		t.Errorf("second run still wanted changes: %v", changes)
	}
	if once != twice {
		t.Error("second run altered the file again")
	}
}
