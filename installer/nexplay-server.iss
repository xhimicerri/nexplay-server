; NEXPLAY Server - Windows installer (Inno Setup 6)
#define AppName "NEXPLAY Server"
#ifndef AppVersion
  #define AppVersion "1.0.1"
#endif

[Setup]
AppId={{6E1B7C2A-4F1D-4C8B-9A64-NEXPLAYSRV01}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher=NEXPLAY
AppPublisherURL=https://nexplaytv.com
AppSupportURL=https://github.com/xhimicerri/nexplay-server
DefaultDirName={autopf}\NEXPLAY Server
DefaultGroupName=NEXPLAY Server
DisableProgramGroupPage=yes
OutputDir=..\dist
OutputBaseFilename=NEXPLAY-Server-Setup-{#AppVersion}
SetupIconFile=nexplay.ico
UninstallDisplayIcon={app}\nexplay-server.exe
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
LicenseFile=license.txt

[Files]
Source: "..\build\nexplay-server.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\third_party\ffmpeg\ffmpeg.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\third_party\ffmpeg\ffprobe.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\third_party\ffmpeg\LICENSE.txt"; DestDir: "{app}\licenses"; DestName: "ffmpeg-LICENSE.txt"; Flags: ignoreversion
Source: "license.txt"; DestDir: "{app}\licenses"; DestName: "NEXPLAY-Server.txt"; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}\licenses"; DestName: "LICENSE.txt"; Flags: ignoreversion

[Icons]
Name: "{group}\NEXPLAY Server"; Filename: "http://localhost:8096/web/"; IconFilename: "{app}\nexplay-server.exe"
Name: "{autodesktop}\NEXPLAY Server"; Filename: "http://localhost:8096/web/"; IconFilename: "{app}\nexplay-server.exe"; Tasks: desktopicon
Name: "{group}\Uninstall NEXPLAY Server"; Filename: "{uninstallexe}"

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut to the server panel"; Flags: checkedonce

[Run]
; stop/remove an older service, install and start the new one (runs in the background, starts with Windows)
Filename: "{app}\nexplay-server.exe"; Parameters: "-service stop"; Flags: runhidden waituntilterminated; StatusMsg: "Stopping the old server..."
Filename: "{app}\nexplay-server.exe"; Parameters: "-service uninstall"; Flags: runhidden waituntilterminated
Filename: "{app}\nexplay-server.exe"; Parameters: "-service install"; Flags: runhidden waituntilterminated; StatusMsg: "Installing the NEXPLAY Server service..."
Filename: "{sys}\sc.exe"; Parameters: "config NexplayServer start= auto"; Flags: runhidden waituntilterminated
Filename: "{sys}\sc.exe"; Parameters: "failure NexplayServer reset= 86400 actions= restart/5000/restart/5000/restart/30000"; Flags: runhidden waituntilterminated
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""NEXPLAY Server"""; Flags: runhidden waituntilterminated
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall add rule name=""NEXPLAY Server"" dir=in action=allow protocol=TCP localport=8096 profile=any"; Flags: runhidden waituntilterminated; StatusMsg: "Allowing the apps on your network..."
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall add rule name=""NEXPLAY Server"" dir=in action=allow protocol=UDP localport=7359 profile=any"; Flags: runhidden waituntilterminated
Filename: "{app}\nexplay-server.exe"; Parameters: "-service start"; Flags: runhidden waituntilterminated; StatusMsg: "Starting NEXPLAY Server..."
Filename: "http://localhost:8096/web/"; Description: "Open NEXPLAY Server to finish the setup"; Flags: postinstall shellexec nowait

[UninstallRun]
Filename: "{app}\nexplay-server.exe"; Parameters: "-service stop"; Flags: runhidden waituntilterminated; RunOnceId: "StopSvc"
Filename: "{app}\nexplay-server.exe"; Parameters: "-service uninstall"; Flags: runhidden waituntilterminated; RunOnceId: "DelSvc"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""NEXPLAY Server"""; Flags: runhidden waituntilterminated; RunOnceId: "DelFw"

[Messages]
FinishedLabel=NEXPLAY Server is installed and running.%n%nYour browser will open the server panel: create the administrator account and choose your media folders.
