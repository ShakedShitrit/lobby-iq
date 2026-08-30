; LobbyIQ installer.
;
; Built by .github/workflows/release.yml and attached to the GitHub release, so
; the download on the releases page is a single setup.exe that needs nothing
; else installed.
;
; Deliberately thin. Everything that has to inspect the machine - finding
; Rocket League, finding a Documents folder that may live in OneDrive, editing
; TAStatsAPI.ini without destroying it - is in the app, behind "lobby-iq setup",
; and is covered by tests. Installer script is a poor place for logic: it only
; runs on a machine that is already committed to installing.
;
; Build:
;   iscc /DAppVersion=1.2.3 installer\lobbyiq.iss

#define AppName "LobbyIQ"
#define AppExeName "lobby-iq.exe"
#define AppPublisher "ShakedShitrit"
#define AppURL "https://github.com/ShakedShitrit/lobby-iq"

; Overridden on the command line by CI; the default keeps a local build working.
#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif

[Setup]
; Never change AppId: it is how Windows recognises an existing install, and a
; new one would turn every upgrade into a second copy in Add/Remove Programs.
AppId={{EAF3C2E8-11B5-4F0C-B0DC-DD392FD1D2E7}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}/issues
AppUpdatesURL={#AppURL}/releases

; Per-user install, which is what makes this a no-prompt, no-admin download.
;
; It is also a correctness requirement, not just a convenience: LobbyIQ keeps
; config.yaml, players.json, self.json and its log beside its own executable,
; and Program Files is not writable by the user the app runs as.
PrivilegesRequired=lowest
DefaultDirName={autopf}\{#AppName}
DisableProgramGroupPage=yes
DisableDirPage=auto

OutputDir=..\dist
OutputBaseFilename=LobbyIQ-Setup-{#AppVersion}
SetupIconFile=..\assets\brand\lobbyiq.ico
UninstallDisplayIcon={app}\{#AppExeName}
WizardStyle=modern
Compression=lzma2/max
SolidCompression=yes

; The app is 64-bit only, and saying so gives a clear refusal rather than an
; install that fails on first launch.
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

; Offer to close a running copy rather than failing on a locked file, which is
; the normal case when upgrading.
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
Source: "..\dist\{#AppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\config.example.yaml"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\DISCORD.md"; DestDir: "{app}"; Flags: ignoreversion

; The working config, seeded from the example the first time only.
;
; onlyifdoesntexist is what makes an upgrade safe: without it, every new
; version would overwrite the Discord IDs and ranks the user had set. It is
; also left behind on uninstall, so reinstalling keeps your settings - the
; uninstaller offers to remove it explicitly instead.
Source: "..\config.example.yaml"; DestDir: "{app}"; DestName: "config.yaml"; \
    Flags: onlyifdoesntexist uninsneveruninstall

[Icons]
Name: "{autoprograms}\{#AppName}"; Filename: "{app}\{#AppExeName}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExeName}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExeName}"; Description: "{cm:LaunchProgram,{#AppName}}"; \
    Flags: nowait postinstall skipifsilent

[UninstallDelete]
; State the app wrote next to itself. Named individually rather than removing
; the directory wholesale, so nothing a user put there is taken with it.
Type: files; Name: "{app}\lobby-iq.log"
Type: files; Name: "{app}\players.json"
Type: files; Name: "{app}\self.json"

[Code]

// ConfigureRocketLeague runs the app's own setup command.
//
// Offered again on failure rather than reported and abandoned: by far the
// likeliest cause is that Rocket League is running, which the user can fix in
// ten seconds without leaving the installer. Giving up would leave them with
// an app that silently shows nothing.
procedure ConfigureRocketLeague();
var
  ResultCode: Integer;
  Message: String;
begin
  repeat
    if not Exec(ExpandConstant('{app}\{#AppExeName}'), 'setup --quiet', '',
                SW_HIDE, ewWaitUntilTerminated, ResultCode) then
    begin
      MsgBox('LobbyIQ was installed, but its Rocket League setup could not be' #13#10
             'started. Open LobbyIQ and it will tell you what is wrong.',
             mbInformation, MB_OK);
      exit;
    end;

    if ResultCode = 0 then
      exit;

    Message :=
      'Rocket League''s match export could not be switched on.' #13#10 #13#10
      'The usual reason is that Rocket League is running: it rewrites its' #13#10
      'settings when it closes, which would undo the change.' #13#10 #13#10
      'Close Rocket League, then choose Retry.';

    if MsgBox(Message, mbError, MB_RETRYCANCEL) = IDCANCEL then
    begin
      MsgBox('Skipped. Once Rocket League is closed, run this installer again' #13#10
             'to finish - or LobbyIQ will do it the next time it starts.',
             mbInformation, MB_OK);
      exit;
    end;
  until False;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  // ssPostInstall rather than the [Run] section, so the exit code can be acted
  // on. [Run] entries report failure only as a generic error, if at all.
  if CurStep = ssPostInstall then
    ConfigureRocketLeague();
end;

// Config is deliberately left behind by [Files], so removing it is offered
// here instead - an uninstall that silently discarded a user's Discord setup
// would be a nasty surprise for anyone reinstalling.
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ConfigPath: String;
begin
  if CurUninstallStep <> usUninstall then
    exit;

  ConfigPath := ExpandConstant('{app}\config.yaml');
  if not FileExists(ConfigPath) then
    exit;

  if MsgBox('Remove your LobbyIQ settings (config.yaml) as well?' #13#10 #13#10
            'Choose No to keep them for a future reinstall.',
            mbConfirmation, MB_YESNO) = IDYES then
    DeleteFile(ConfigPath);
end;
