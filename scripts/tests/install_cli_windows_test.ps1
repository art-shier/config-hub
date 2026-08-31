$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2

function Assert-Equal {
    param(
        [Parameter(Mandatory = $true)][string]$Expected,
        [Parameter(Mandatory = $true)][AllowEmptyString()][object]$Actual
    )

    if ($Expected -cne [string]$Actual) {
        throw "Expected [$Expected], got [$Actual]"
    }
}

function Assert-Throws {
    param([Parameter(Mandatory = $true)][scriptblock]$Action)

    $threw = $false
    try {
        & $Action
    }
    catch {
        $threw = $true
    }
    if (-not $threw) {
        throw 'Expected the action to throw'
    }
}

function Assert-True {
    param(
        [Parameter(Mandatory = $true)][bool]$Condition,
        [Parameter(Mandatory = $true)][string]$Message
    )

    if (-not $Condition) {
        throw $Message
    }
}

function New-ZipFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][hashtable[]]$Entries
    )

    if ([IO.File]::Exists($Path)) {
        [IO.File]::Delete($Path)
    }
    $zip = [IO.Compression.ZipFile]::Open($Path, [IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($entrySpec in $Entries) {
            $entry = $zip.CreateEntry([string]$entrySpec.Name)
            if ($entrySpec.ContainsKey('ExternalAttributes')) {
                $entry.ExternalAttributes = [int]$entrySpec.ExternalAttributes
            }
            if (-not ([string]$entrySpec.Name).EndsWith('/')) {
                if ($entrySpec.ContainsKey('Bytes')) {
                    $bytes = [byte[]]$entrySpec.Bytes
                }
                else {
                    $content = ''
                    if ($entrySpec.ContainsKey('Content')) {
                        $content = [string]$entrySpec.Content
                    }
                    $bytes = [Text.Encoding]::ASCII.GetBytes($content)
                }
                $stream = $entry.Open()
                try {
                    $stream.Write($bytes, 0, $bytes.Length)
                }
                finally {
                    $stream.Dispose()
                }
            }
        }
    }
    finally {
        $zip.Dispose()
    }
}

function Assert-ArchiveRejected {
    param(
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$ArchiveBase,
        [Parameter(Mandatory = $true)][string]$StagingPath,
        [Parameter(Mandatory = $true)][string]$ExistingTarget
    )

    if ([IO.File]::Exists($StagingPath)) {
        [IO.File]::Delete($StagingPath)
    }
    $script:internetMarkCalls = 0
    Assert-Throws {
        Expand-VerifiedCliArchive -ArchivePath $ArchivePath `
            -ArchiveBase $ArchiveBase -StagingPath $StagingPath
        Remove-InternetMark -Path $StagingPath
    }
    Assert-Equal '0' $script:internetMarkCalls
    Assert-Equal 'old-cli' ([Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes($ExistingTarget)))
}

function Get-PathEntryCount {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$PathValue,
        [Parameter(Mandatory = $true)][string]$ExpectedEntry
    )

    $expectedFullPath = [IO.Path]::GetFullPath($ExpectedEntry).TrimEnd('\')
    $count = 0
    foreach ($entry in $PathValue.Split([IO.Path]::PathSeparator)) {
        if ([string]::IsNullOrWhiteSpace($entry)) {
            continue
        }
        $entryFullPath = [IO.Path]::GetFullPath($entry).TrimEnd('\')
        if ([string]::Equals($entryFullPath, $expectedFullPath, [StringComparison]::OrdinalIgnoreCase)) {
            $count++
        }
    }
    return $count
}

$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
$installerPath = Join-Path $repoRoot 'scripts\install-cli.ps1'

# An invalid requested version makes an accidental main invocation fail safely,
# while correct dot-sourcing only defines the installer functions.
. $installerPath -Version 'dot-source-must-not-install' -InstallDir $PSScriptRoot

Assert-Equal 'windows amd64' (Resolve-WindowsCliTarget -IsWindows $true -Architecture 'AMD64' -Wow64Architecture '')
Assert-Equal 'windows amd64' (Resolve-WindowsCliTarget -IsWindows $true -Architecture 'x86' -Wow64Architecture 'AMD64')
Assert-Throws { Resolve-WindowsCliTarget -IsWindows $false -Architecture 'AMD64' -Wow64Architecture '' }
Assert-Throws { Resolve-WindowsCliTarget -IsWindows $true -Architecture 'ARM64' -Wow64Architecture '' }
Assert-ReleaseTag 'v1.2.3'
Assert-Throws { Assert-ReleaseTag 'v1.2.3-rc1' }
Assert-Throws { Assert-AbsoluteInstallDirectory 'relative\bin' }
Assert-Equal 'v1.2.3' (Get-ReleaseTagFromUri ([Uri]'https://github.com/art-shier/config-hub/releases/tag/v1.2.3'))
Assert-Throws { Get-ReleaseTagFromUri ([Uri]'https://github.com/another/config-hub/releases/tag/v1.2.3') }
Assert-Throws { Get-ReleaseTagFromUri ([Uri]'https://github.com/art-shier/config-hub/releases/tag/v1.2.3-rc1') }

Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ("confighub-cli-windows-test-{0}" -f [Guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($fixtureRoot)
$savedProcessPath = $env:Path
try {
    $archiveBase = 'config-hub-cli_1.2.3_windows_amd64'
    $archiveName = "$archiveBase.zip"
    $validArchive = Join-Path $fixtureRoot $archiveName
    New-ZipFixture -Path $validArchive -Entries @(
        @{ Name = "$archiveBase/" },
        @{ Name = "$archiveBase/confighub.exe"; Content = 'fixture-cli' }
    )

    $digest = (Get-FileHash -LiteralPath $validArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    $validManifest = Join-Path $fixtureRoot 'checksums.txt'
    [IO.File]::WriteAllText($validManifest, "$digest  $archiveName`n", [Text.Encoding]::ASCII)
    Assert-Equal $digest (Get-ManifestDigest -ManifestPath $validManifest -ArchiveName $archiveName)
    Assert-ArchiveChecksum -ManifestPath $validManifest -ArchivePath $validArchive -ArchiveName $archiveName

    $wrongManifest = Join-Path $fixtureRoot 'checksums-wrong.txt'
    [IO.File]::WriteAllText($wrongManifest, "$('0' * 64)  $archiveName`n", [Text.Encoding]::ASCII)
    Assert-Throws { Assert-ArchiveChecksum -ManifestPath $wrongManifest -ArchivePath $validArchive -ArchiveName $archiveName }

    $missingManifest = Join-Path $fixtureRoot 'checksums-missing.txt'
    [IO.File]::WriteAllText($missingManifest, "$digest  another.zip`n", [Text.Encoding]::ASCII)
    Assert-Throws { Get-ManifestDigest -ManifestPath $missingManifest -ArchiveName $archiveName }

    $duplicateManifest = Join-Path $fixtureRoot 'checksums-duplicate.txt'
    [IO.File]::WriteAllText(
        $duplicateManifest,
        "$digest  $archiveName`n$digest  $archiveName`n",
        [Text.Encoding]::ASCII
    )
    Assert-Throws { Get-ManifestDigest -ManifestPath $duplicateManifest -ArchiveName $archiveName }

    $installRoot = Join-Path $fixtureRoot 'install'
    [void][IO.Directory]::CreateDirectory($installRoot)
    $stagingPath = Join-Path $installRoot '.confighub.install.test.exe'
    $existingTarget = Join-Path $installRoot 'confighub.exe'
    [IO.File]::WriteAllBytes($existingTarget, [Text.Encoding]::ASCII.GetBytes('old-cli'))

    Expand-VerifiedCliArchive -ArchivePath $validArchive `
        -ArchiveBase $archiveBase -StagingPath $stagingPath
    Assert-True ([IO.File]::Exists($stagingPath)) 'Expected the validated executable to be staged.'
    Assert-Equal 'fixture-cli' ([Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes($stagingPath)))
    Remove-InternetMark -Path $stagingPath
    [IO.File]::Delete($stagingPath)

    $script:internetMarkCalls = 0
    function Remove-InternetMark {
        param([Parameter(Mandatory = $true)][string]$Path)
        $script:internetMarkCalls++
    }

    $extraArchive = Join-Path $fixtureRoot 'extra.zip'
    New-ZipFixture -Path $extraArchive -Entries @(
        @{ Name = "$archiveBase/" },
        @{ Name = "$archiveBase/confighub.exe"; Content = 'fixture-cli' },
        @{ Name = "$archiveBase/extra.txt"; Content = 'extra' }
    )
    Assert-ArchiveRejected $extraArchive $archiveBase $stagingPath $existingTarget

    $traversalArchive = Join-Path $fixtureRoot 'traversal.zip'
    New-ZipFixture -Path $traversalArchive -Entries @(
        @{ Name = "$archiveBase/" },
        @{ Name = '../confighub.exe'; Content = 'unsafe' }
    )
    Assert-ArchiveRejected $traversalArchive $archiveBase $stagingPath $existingTarget

    $rootedArchive = Join-Path $fixtureRoot 'rooted.zip'
    New-ZipFixture -Path $rootedArchive -Entries @(
        @{ Name = "$archiveBase/" },
        @{ Name = '/confighub.exe'; Content = 'unsafe' }
    )
    Assert-ArchiveRejected $rootedArchive $archiveBase $stagingPath $existingTarget

    $duplicateArchive = Join-Path $fixtureRoot 'duplicate.zip'
    New-ZipFixture -Path $duplicateArchive -Entries @(
        @{ Name = "$archiveBase/" },
        @{ Name = "$archiveBase/confighub.exe"; Content = 'first' },
        @{ Name = "$archiveBase/confighub.exe"; Content = 'second' }
    )
    Assert-ArchiveRejected $duplicateArchive $archiveBase $stagingPath $existingTarget

    $linkArchive = Join-Path $fixtureRoot 'link.zip'
    New-ZipFixture -Path $linkArchive -Entries @(
        @{ Name = "$archiveBase/" },
        @{
            Name = "$archiveBase/confighub.exe"
            Content = 'unsafe'
            ExternalAttributes = [int](0xA000 -shl 16)
        }
    )
    Assert-ArchiveRejected $linkArchive $archiveBase $stagingPath $existingTarget

    $fixtureExe = Join-Path $fixtureRoot 'fixture-confighub.exe'
    $fixtureSource = @'
using System;
using System.IO;
public static class Program {
    public static int Main(string[] args) {
        if (args.Length == 1 && args[0] == "version") {
            string eventLog = Environment.GetEnvironmentVariable("CONFIGHUB_CLI_TEST_EVENT_LOG");
            if (!String.IsNullOrEmpty(eventLog)) {
                File.AppendAllText(eventLog, "version" + Environment.NewLine);
            }
            Console.WriteLine("v1.2.3");
            return 0;
        }
        return 2;
    }
}
'@
    Add-Type -TypeDefinition $fixtureSource -Language CSharp `
        -OutputAssembly $fixtureExe -OutputType ConsoleApplication

    $wrongFixtureExe = Join-Path $fixtureRoot 'fixture-confighub-wrong.exe'
    $wrongFixtureSource = @'
using System;
using System.IO;
namespace WrongFixture {
    public static class Program {
        public static int Main(string[] args) {
            if (args.Length == 1 && args[0] == "version") {
                string eventLog = Environment.GetEnvironmentVariable("CONFIGHUB_CLI_TEST_EVENT_LOG");
                if (!String.IsNullOrEmpty(eventLog)) {
                    File.AppendAllText(eventLog, "version" + Environment.NewLine);
                }
                Console.WriteLine("v9.9.9");
                return 0;
            }
            return 2;
        }
    }
}
'@
    Add-Type -TypeDefinition $wrongFixtureSource -Language CSharp `
        -OutputAssembly $wrongFixtureExe -OutputType ConsoleApplication

    function Set-DownloadArchive {
        param([Parameter(Mandatory = $true)][string]$ExecutablePath)

        New-ZipFixture -Path $validArchive -Entries @(
            @{ Name = "$archiveBase/" },
            @{
                Name = "$archiveBase/confighub.exe"
                Bytes = [IO.File]::ReadAllBytes($ExecutablePath)
            }
        )
        $archiveDigest = (Get-FileHash -LiteralPath $validArchive -Algorithm SHA256).Hash.ToLowerInvariant()
        [IO.File]::WriteAllText($validManifest, "$archiveDigest  $archiveName`n", [Text.Encoding]::ASCII)
    }

    $script:downloadFixtureRoot = $fixtureRoot
    function Invoke-DownloadFile {
        param(
            [Parameter(Mandatory = $true)][Uri]$Uri,
            [Parameter(Mandatory = $true)][string]$OutputPath
        )

        $sourceName = [IO.Path]::GetFileName($Uri.AbsolutePath)
        [IO.File]::Copy((Join-Path $script:downloadFixtureRoot $sourceName), $OutputPath, $false)
    }

    $script:testUserPath = 'C:\Existing\bin'
    function Get-UserPathValue {
        return $script:testUserPath
    }
    function Set-UserPathValue {
        param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)
        $script:testUserPath = $Value
    }

    $installEventLog = Join-Path $fixtureRoot 'install-events.txt'
    $env:CONFIGHUB_CLI_TEST_EVENT_LOG = $installEventLog
    function Remove-InternetMark {
        param([Parameter(Mandatory = $true)][string]$Path)

        Assert-True ([IO.File]::Exists($Path)) 'Internet mark target must exist.'
        [IO.File]::AppendAllText($installEventLog, "origin-mark`r`n", [Text.Encoding]::ASCII)
    }

    Set-DownloadArchive $fixtureExe
    if ([IO.File]::Exists($installEventLog)) {
        [IO.File]::Delete($installEventLog)
    }
    [IO.File]::WriteAllBytes($existingTarget, [Text.Encoding]::ASCII.GetBytes('old-cli'))
    $successMessage = Install-CliRelease -Tag 'v1.2.3' -InstallDirectory $installRoot
    Assert-Equal "Installed ConfigHub CLI v1.2.3 to $existingTarget" $successMessage
    Assert-True ([IO.File]::Exists($existingTarget)) 'Expected confighub.exe to be installed.'
    $installedAttributes = [IO.File]::GetAttributes($existingTarget)
    Assert-True (($installedAttributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) `
        'Installed confighub.exe must not be a reparse point.'
    Assert-Equal 'v1.2.3' (& $existingTarget version)
    Assert-Equal '0' $LASTEXITCODE
    $installEvents = [IO.File]::ReadAllLines($installEventLog)
    Assert-True ($installEvents.Length -ge 2) 'Expected origin-mark and version events.'
    Assert-Equal 'origin-mark' $installEvents[0]
    Assert-Equal 'version' $installEvents[1]
    Assert-Equal '1' (Get-PathEntryCount $script:testUserPath $installRoot)
    Assert-Equal '1' (Get-PathEntryCount $env:Path $installRoot)

    $userPathAfterSuccess = $script:testUserPath
    $processPathAfterSuccess = $env:Path
    Install-CliRelease -Tag 'v1.2.3' -InstallDirectory $installRoot | Out-Null
    Assert-Equal $userPathAfterSuccess $script:testUserPath
    Assert-Equal $processPathAfterSuccess $env:Path
    Assert-Equal '1' (Get-PathEntryCount $script:testUserPath $installRoot)
    Assert-Equal '1' (Get-PathEntryCount $env:Path $installRoot)

    [IO.File]::WriteAllBytes($existingTarget, [Text.Encoding]::ASCII.GetBytes('old-cli'))
    [IO.File]::WriteAllText($validManifest, "$('0' * 64)  $archiveName`n", [Text.Encoding]::ASCII)
    if ([IO.File]::Exists($installEventLog)) {
        [IO.File]::Delete($installEventLog)
    }
    $userPathBeforeFailure = $script:testUserPath
    $processPathBeforeFailure = $env:Path
    Assert-Throws { Install-CliRelease -Tag 'v1.2.3' -InstallDirectory $installRoot }
    Assert-Equal 'old-cli' ([Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes($existingTarget)))
    Assert-True (-not [IO.File]::Exists($installEventLog)) 'Wrong checksum must fail before marker removal.'
    Assert-Equal $userPathBeforeFailure $script:testUserPath
    Assert-Equal $processPathBeforeFailure $env:Path

    Set-DownloadArchive $wrongFixtureExe
    if ([IO.File]::Exists($installEventLog)) {
        [IO.File]::Delete($installEventLog)
    }
    Assert-Throws { Install-CliRelease -Tag 'v1.2.3' -InstallDirectory $installRoot }
    Assert-Equal 'old-cli' ([Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes($existingTarget)))
    Assert-Equal $userPathBeforeFailure $script:testUserPath
    Assert-Equal $processPathBeforeFailure $env:Path

    Set-DownloadArchive $fixtureExe
    if ([IO.File]::Exists($installEventLog)) {
        [IO.File]::Delete($installEventLog)
    }
    $realMoveFileAtomically = (Get-Command Move-FileAtomically -CommandType Function).ScriptBlock
    function Move-FileAtomically {
        param(
            [Parameter(Mandatory = $true)][string]$SourcePath,
            [Parameter(Mandatory = $true)][string]$DestinationPath
        )
        throw 'simulated atomic move failure'
    }
    try {
        Assert-Throws { Install-CliRelease -Tag 'v1.2.3' -InstallDirectory $installRoot }
    }
    finally {
        Set-Item -Path Function:\Move-FileAtomically -Value $realMoveFileAtomically
    }
    Assert-Equal 'old-cli' ([Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes($existingTarget)))
    Assert-Equal $userPathBeforeFailure $script:testUserPath
    Assert-Equal $processPathBeforeFailure $env:Path
}
finally {
    $env:Path = $savedProcessPath
    Remove-Item Env:\CONFIGHUB_CLI_TEST_EVENT_LOG -ErrorAction SilentlyContinue
    if ([IO.Directory]::Exists($fixtureRoot)) {
        Remove-Item -LiteralPath $fixtureRoot -Recurse -Force
    }
}

Write-Output 'Windows CLI installer tests passed'
