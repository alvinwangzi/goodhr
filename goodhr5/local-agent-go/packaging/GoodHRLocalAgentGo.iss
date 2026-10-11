; Purpose: build the HRPlus Go Local Agent Windows installer with Inno Setup.
#ifndef BuildEnvironment
  #error "BuildEnvironment must be dev or prod"
#endif
#if (BuildEnvironment != "dev") && (BuildEnvironment != "prod")
  #error "BuildEnvironment must be dev or prod"
#endif
#define MyAppName "HR+"
#ifndef MyAppVersion
#define MyAppVersion "0.1.11"
#endif
#define MyAppPublisher "HR+"
#define MyAppExeName "hrplus-agent.exe"

[Setup]
AppId={{A7F8D98D-9D3D-47E7-A1F6-50F333A1F6D2}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
DefaultDirName={localappdata}\Programs\HRPlus
DefaultGroupName=HRPlus
DisableProgramGroupPage=yes
OutputDir=..\dist\installers\{#BuildEnvironment}
OutputBaseFilename=HRPlusSetup-{#BuildEnvironment}-{#MyAppVersion}
Compression=zip
SolidCompression=no
WizardStyle=modern
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
SetupIconFile=..\assets\icons\goodhr-logo.ico
UninstallDisplayIcon={app}\goodhr-logo.ico
CloseApplications=no
RestartApplications=no

[Languages]
Name: "chinesesimplified"; MessagesFile: ".\ChineseSimplified.isl"

[InstallDelete]
; 升级安装时彻底删除旧 Worker，防止旧文件与新主程序混用。
Type: filesandordirs; Name: "{app}\worker-node"
Type: filesandordirs; Name: "{app}\resources\worker-node"
Type: filesandordirs; Name: "{userappdata}\HRPlus\runtime\browser-worker"
; 清理历史版本残留的旧 exe，防止升级后安装目录同时存在新旧两个主程序。
Type: files; Name: "{app}\goodhr-local-agent.exe"

[Files]
Source: "..\dist\installer-input\{#BuildEnvironment}\{#MyAppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\assets\icons\goodhr-logo.ico"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\audio\*"; DestDir: "{app}\audio"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "..\dist\installer-input\{#BuildEnvironment}\worker-node\*"; DestDir: "{app}\worker-node"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "..\dist\installer-input\{#BuildEnvironment}\console\*"; DestDir: "{userappdata}\HRPlus\console"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{autoprograms}\HR+"; Filename: "{app}\{#MyAppExeName}"; IconFilename: "{app}\goodhr-logo.ico"
Name: "{autodesktop}\HR+"; Filename: "{app}\{#MyAppExeName}"; IconFilename: "{app}\goodhr-logo.ico"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式（请务必勾选）"; GroupDescription: "快捷方式："

[Run]
; 静默安装仅更新文件，避免未经交互选择就恢复用户已有招聘任务。
Filename: "{app}\{#MyAppExeName}"; Parameters: "--restart"; Description: "启动 HR+"; Flags: nowait postinstall skipifsilent
; 刷新 Windows 图标缓存，确保桌面快捷方式立即显示新图标。
Filename: "ie4uinit.exe"; Parameters: "-show"; Flags: runhidden postinstall skipifsilent

[Code]
// StopProcessByImageName 静默结束指定进程，避免升级安装时文件被旧程序占用。
procedure StopProcessByImageName(ImageName: String);
var
  ResultCode: Integer;
begin
  Exec(ExpandConstant('{cmd}'), '/C taskkill /IM "' + ImageName + '" /T /F >NUL 2>NUL', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

// CurStepChanged 在安装文件复制前清理旧进程，避免出现占用文件弹窗。
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssInstall then
  begin
    StopProcessByImageName('hrplus-agent.exe');
    StopProcessByImageName('goodhr-local-agent.exe');
    StopProcessByImageName('XtaCache.exe');
    StopProcessByImageName('XtaCache');
  end;
end;
