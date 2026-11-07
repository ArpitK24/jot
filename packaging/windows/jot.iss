; Inno Setup script for the jot Windows installer.
; Build: iscc /DAppVersion=0.3.0 /DArch=x64 /DSourceDir=<folder with jot.exe, README.md, LICENSE>
;        /DDesktopExe=<path to jot-desktop.exe> packaging\windows\jot.iss
; Installs per user (no admin prompt): the jot desktop app, the jot CLI on
; the user PATH, and a Start Menu entry. The app's first run connects AI tools.

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef NumVersion
  #define NumVersion "0.0.0"
#endif
#ifndef Arch
  #define Arch "x64"
#endif
#ifndef SourceDir
  #error SourceDir must point at the folder holding jot.exe
#endif
#ifndef DesktopExe
  #error DesktopExe must point at jot-desktop.exe
#endif
#ifndef OutputDir
  #define OutputDir "..\..\dist"
#endif

[Setup]
AppId={{6C7E3F0A-4B9D-4E52-9A51-2F7B1C0D8E3A}
AppName=jot
AppVersion={#AppVersion}
AppVerName=jot {#AppVersion}
VersionInfoVersion={#NumVersion}
VersionInfoProductName=jot
AppPublisher=Arpit Kohale
AppPublisherURL=https://github.com/ArpitK24/jot
AppSupportURL=https://github.com/ArpitK24/jot/issues
AppUpdatesURL=https://github.com/ArpitK24/jot/releases
DefaultDirName={localappdata}\Programs\jot
DisableProgramGroupPage=yes
DisableDirPage=auto
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ChangesEnvironment=yes
ArchitecturesAllowed={#Arch}compatible
ArchitecturesInstallIn64BitMode={#Arch}compatible
OutputDir={#OutputDir}
OutputBaseFilename=jot-{#AppVersion}-windows-{#Arch}-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
LicenseFile=..\..\LICENSE
UninstallDisplayName=jot {#AppVersion}
UninstallDisplayIcon={app}\jot-desktop.exe
SetupIconFile=..\..\desktop\build\windows\icon.ico
CloseApplications=yes
RestartApplications=no

[Tasks]
Name: "addtopath"; Description: "Add the jot command to my PATH (use it from any terminal)"
Name: "desktopicon"; Description: "Create a desktop shortcut"; Flags: unchecked

[Files]
Source: "{#DesktopExe}"; DestDir: "{app}"; DestName: "jot-desktop.exe"; Flags: ignoreversion
Source: "{#SourceDir}\jot.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\jot"; Filename: "{app}\jot-desktop.exe"; Comment: "Shared memory for your AI tools"
Name: "{autodesktop}\jot"; Filename: "{app}\jot-desktop.exe"; Tasks: desktopicon

[Run]
Filename: "{app}\jot.exe"; Parameters: "init"; Flags: runhidden; StatusMsg: "Creating your memory store..."
Filename: "{app}\jot-desktop.exe"; Description: "Open jot"; Flags: nowait postinstall skipifsilent
; The app's own updater runs setup silently with /RELAUNCH: open jot again when done.
Filename: "{app}\jot-desktop.exe"; Flags: nowait; Check: Relaunch

[UninstallRun]
Filename: "{app}\jot.exe"; Parameters: "setup --undo"; Flags: runhidden; RunOnceId: "jotSetupUndo"

[Messages]
FinishedLabel=jot is installed.%n%nOpen jot to connect your AI tools. The jot command works in any new terminal.%n%nYour notes live in %USERPROFILE%\.jot. Uninstalling never deletes them.

[Code]
const
  EnvKey = 'Environment';

function Relaunch: Boolean;
begin
  Result := WizardSilent and (Pos('/RELAUNCH', Uppercase(GetCmdTail)) > 0);
end;

function PathHas(Paths, Dir: string): Boolean;
begin
  Result := Pos(';' + Uppercase(Dir) + ';', ';' + Uppercase(Paths) + ';') > 0;
end;

procedure AddToPath(Dir: string);
var
  Paths: string;
begin
  if not RegQueryStringValue(HKCU, EnvKey, 'Path', Paths) then
    Paths := '';
  if PathHas(Paths, Dir) then
    exit;
  if (Paths <> '') and (Copy(Paths, Length(Paths), 1) <> ';') then
    Paths := Paths + ';';
  RegWriteExpandStringValue(HKCU, EnvKey, 'Path', Paths + Dir);
end;

procedure RemoveFromPath(Dir: string);
var
  Paths: string;
  P: Integer;
begin
  if not RegQueryStringValue(HKCU, EnvKey, 'Path', Paths) then
    exit;
  Paths := ';' + Paths + ';';
  P := Pos(';' + Uppercase(Dir) + ';', Uppercase(Paths));
  if P = 0 then
    exit;
  Delete(Paths, P, Length(Dir) + 1);
  Paths := Copy(Paths, 2, Length(Paths) - 2);
  RegWriteExpandStringValue(HKCU, EnvKey, 'Path', Paths);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if (CurStep = ssPostInstall) and WizardIsTaskSelected('addtopath') then
    AddToPath(ExpandConstant('{app}'));
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
    RemoveFromPath(ExpandConstant('{app}'));
end;
