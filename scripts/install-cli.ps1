[CmdletBinding()]
param(
    [string]$Version = '',
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'ConfigHub\bin')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2

$script:ConfigHubWebRoot = 'https://github.com/art-shier/config-hub'
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
if ($null -eq ('ConfigHubInstaller.NativeMethods' -as [type])) {
    Add-Type -TypeDefinition @'
using System.Runtime.InteropServices;

namespace ConfigHubInstaller {
    public static class NativeMethods {
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        public static extern bool MoveFileExW(
            string existingFileName,
            string newFileName,
            uint flags
        );
    }
}
'@
}

function Assert-ReleaseTag {
    param([Parameter(Mandatory = $true)][string]$Tag)

    if ($Tag -cnotmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
        throw 'Version must match vMAJOR.MINOR.PATCH.'
    }
}

function Assert-AbsoluteInstallDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)

    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -cnotmatch '^[A-Za-z]:[\\/]') {
        throw 'Install directory must be an absolute local Windows path.'
    }
    [void][IO.Path]::GetFullPath($Path)
}

function Resolve-WindowsCliTarget {
    param(
        [Parameter(Mandatory = $true)][bool]$IsWindows,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Architecture,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Wow64Architecture
    )

    if (-not $IsWindows) {
        throw 'The ConfigHub Windows installer only supports Windows.'
    }

    $processArchitecture = $Architecture.ToUpperInvariant()
    $nativeArchitecture = $Wow64Architecture.ToUpperInvariant()
    if ($processArchitecture -ceq 'AMD64' -or
        ($processArchitecture -ceq 'X86' -and $nativeArchitecture -ceq 'AMD64')) {
        return 'windows amd64'
    }

    throw "Unsupported Windows architecture: $Architecture/$Wow64Architecture"
}

function Get-ReleaseTagFromUri {
    param([Parameter(Mandatory = $true)][Uri]$Uri)

    if ($Uri.Scheme -cne 'https' -or
        -not [string]::Equals($Uri.Host, 'github.com', [StringComparison]::OrdinalIgnoreCase) -or
        -not [string]::IsNullOrEmpty($Uri.Query) -or
        -not [string]::IsNullOrEmpty($Uri.Fragment) -or
        $Uri.AbsolutePath -cnotmatch '^/art-shier/config-hub/releases/tag/([^/]+)$') {
        throw 'Latest release redirected outside the ConfigHub repository.'
    }

    $tag = $Matches[1]
    Assert-ReleaseTag $tag
    return $tag
}

function Enable-Tls12 {
    [Net.ServicePointManager]::SecurityProtocol =
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

function Get-LatestReleaseTag {
    Enable-Tls12
    $response = Invoke-WebRequest -Uri "$script:ConfigHubWebRoot/releases/latest" `
        -MaximumRedirection 10 -UseBasicParsing
    $baseResponse = $response.BaseResponse
    $effectiveUri = $null
    if ($null -ne $baseResponse.PSObject.Properties['ResponseUri']) {
        $effectiveUri = $baseResponse.ResponseUri
    }
    elseif ($null -ne $baseResponse.PSObject.Properties['RequestMessage'] -and
        $null -ne $baseResponse.RequestMessage) {
        $effectiveUri = $baseResponse.RequestMessage.RequestUri
    }
    if ($null -eq $effectiveUri) {
        throw 'Could not determine the latest GitHub Release URL.'
    }
    return Get-ReleaseTagFromUri ([Uri]$effectiveUri)
}

function Invoke-DownloadFile {
    param(
        [Parameter(Mandatory = $true)][Uri]$Uri,
        [Parameter(Mandatory = $true)][string]$OutputPath
    )

    Enable-Tls12
    Invoke-WebRequest -Uri $Uri -OutFile $OutputPath -UseBasicParsing | Out-Null
}

function Get-ManifestDigest {
    param(
        [Parameter(Mandatory = $true)][string]$ManifestPath,
        [Parameter(Mandatory = $true)][string]$ArchiveName
    )

    if ([IO.Path]::GetFileName($ArchiveName) -cne $ArchiveName) {
        throw 'Archive name must be a basename.'
    }

    $pattern = '^([0-9A-Fa-f]{64})  ' + [Regex]::Escape($ArchiveName) + '$'
    $digests = New-Object 'Collections.Generic.List[string]'
    foreach ($line in [IO.File]::ReadAllLines($ManifestPath)) {
        if ($line -cmatch $pattern) {
            $digests.Add($Matches[1])
        }
    }

    if ($digests.Count -ne 1) {
        throw 'Checksum manifest must contain one exact archive entry.'
    }
    return $digests[0].ToLowerInvariant()
}

function Assert-ArchiveChecksum {
    param(
        [Parameter(Mandatory = $true)][string]$ManifestPath,
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$ArchiveName
    )

    $expected = Get-ManifestDigest -ManifestPath $ManifestPath -ArchiveName $ArchiveName
    $actual = (Get-FileHash -LiteralPath $ArchivePath -Algorithm SHA256).Hash
    if (-not [string]::Equals($expected, $actual, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Archive checksum verification failed.'
    }
}

function Expand-VerifiedCliArchive {
    param(
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$ArchiveBase,
        [Parameter(Mandatory = $true)][string]$StagingPath
    )

    $expectedDirectory = "$ArchiveBase/"
    $expectedExecutable = "$ArchiveBase/confighub.exe"
    $seenNames = New-Object 'Collections.Generic.List[string]'
    $executableEntry = $null
    $stagingComplete = $false
    $zip = [IO.Compression.ZipFile]::OpenRead($ArchivePath)
    try {
        foreach ($entry in $zip.Entries) {
            $fullName = $entry.FullName
            if ([string]::IsNullOrEmpty($fullName) -or
                $fullName.Contains('\') -or
                [IO.Path]::IsPathRooted($fullName)) {
                throw "CLI archive contains an unsafe entry: $fullName"
            }
            foreach ($segment in $fullName.Split([char]'/')) {
                if ($segment -ceq '..') {
                    throw "CLI archive contains an unsafe entry: $fullName"
                }
            }
            if ($seenNames.Contains($fullName)) {
                throw "CLI archive contains a duplicate entry: $fullName"
            }
            $seenNames.Add($fullName)

            $unixFileType = ($entry.ExternalAttributes -shr 16) -band 0xF000
            if ($fullName -ceq $expectedDirectory) {
                if ($entry.Length -ne 0 -or ($unixFileType -ne 0 -and $unixFileType -ne 0x4000)) {
                    throw 'CLI archive directory entry is invalid.'
                }
            }
            elseif ($fullName -ceq $expectedExecutable) {
                if ($unixFileType -ne 0 -and $unixFileType -ne 0x8000) {
                    throw 'CLI archive executable entry is not a regular file.'
                }
                $executableEntry = $entry
            }
            else {
                throw "CLI archive contains an unexpected entry: $fullName"
            }
        }

        if ($seenNames.Count -ne 2 -or $null -eq $executableEntry -or
            -not $seenNames.Contains($expectedDirectory)) {
            throw 'CLI archive entries are missing or duplicated.'
        }

        $inputStream = $executableEntry.Open()
        try {
            $outputStream = [IO.File]::Open(
                $StagingPath,
                [IO.FileMode]::CreateNew,
                [IO.FileAccess]::Write,
                [IO.FileShare]::None
            )
            try {
                $inputStream.CopyTo($outputStream)
                $outputStream.Flush()
            }
            finally {
                $outputStream.Dispose()
            }
        }
        finally {
            $inputStream.Dispose()
        }
        $stagingComplete = $true
    }
    finally {
        $zip.Dispose()
        if (-not $stagingComplete -and [IO.File]::Exists($StagingPath)) {
            [IO.File]::Delete($StagingPath)
        }
    }
}

function Remove-InternetMark {
    param([Parameter(Mandatory = $true)][string]$Path)

    Unblock-File -LiteralPath $Path
}

function Assert-CliVersion {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$ExpectedTag
    )

    $output = @(& $Path version 2>$null)
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0 -or $output.Count -ne 1 -or [string]$output[0] -cne $ExpectedTag) {
        throw "Downloaded CLI reported an unexpected version for $ExpectedTag."
    }
}

function Move-FileAtomically {
    param(
        [Parameter(Mandatory = $true)][string]$SourcePath,
        [Parameter(Mandatory = $true)][string]$DestinationPath
    )

    $moveFileReplaceExisting = [uint32]0x1
    $moveFileWriteThrough = [uint32]0x8
    $flags = $moveFileReplaceExisting -bor $moveFileWriteThrough
    if (-not [ConfigHubInstaller.NativeMethods]::MoveFileExW($SourcePath, $DestinationPath, $flags)) {
        $errorCode = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
        $message = (New-Object ComponentModel.Win32Exception($errorCode)).Message
        throw "Atomic CLI installation failed (Win32 $errorCode): $message"
    }
}

function Get-UserPathValue {
    $value = [Environment]::GetEnvironmentVariable('Path', [EnvironmentVariableTarget]::User)
    if ($null -eq $value) {
        return ''
    }
    return [string]$value
}

function Set-UserPathValue {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)

    [Environment]::SetEnvironmentVariable('Path', $Value, [EnvironmentVariableTarget]::User)
}

function Add-UserPathEntry {
    param([Parameter(Mandatory = $true)][string]$InstallDirectory)

    $installFullPath = [IO.Path]::GetFullPath($InstallDirectory)
    $installRoot = [IO.Path]::GetPathRoot($installFullPath)
    if ($installFullPath.Length -gt $installRoot.Length) {
        $installFullPath = $installFullPath.TrimEnd([char[]]@('\', '/'))
    }

    $userPath = [string](Get-UserPathValue)
    $userEntries = New-Object 'Collections.Generic.List[string]'
    $userContainsInstall = $false
    foreach ($entry in $userPath.Split([IO.Path]::PathSeparator)) {
        if ([string]::IsNullOrWhiteSpace($entry)) {
            continue
        }
        $isInstallEntry = $false
        try {
            $entryFullPath = [IO.Path]::GetFullPath($entry)
            $entryRoot = [IO.Path]::GetPathRoot($entryFullPath)
            if ($entryFullPath.Length -gt $entryRoot.Length) {
                $entryFullPath = $entryFullPath.TrimEnd([char[]]@('\', '/'))
            }
            $isInstallEntry = [string]::Equals(
                $entryFullPath,
                $installFullPath,
                [StringComparison]::OrdinalIgnoreCase
            )
        }
        catch {
            $isInstallEntry = $false
        }
        if ($isInstallEntry) {
            if (-not $userContainsInstall) {
                $userEntries.Add($entry)
                $userContainsInstall = $true
            }
        }
        else {
            $userEntries.Add($entry)
        }
    }
    if (-not $userContainsInstall) {
        $userEntries.Add($installFullPath)
    }
    $newUserPath = [string]::Join([string][IO.Path]::PathSeparator, $userEntries.ToArray())
    if (-not [string]::Equals($newUserPath, $userPath, [StringComparison]::Ordinal)) {
        Set-UserPathValue $newUserPath
    }

    $processPath = [string]$env:Path
    $processEntries = New-Object 'Collections.Generic.List[string]'
    $processContainsInstall = $false
    foreach ($entry in $processPath.Split([IO.Path]::PathSeparator)) {
        if ([string]::IsNullOrWhiteSpace($entry)) {
            continue
        }
        $isInstallEntry = $false
        try {
            $entryFullPath = [IO.Path]::GetFullPath($entry)
            $entryRoot = [IO.Path]::GetPathRoot($entryFullPath)
            if ($entryFullPath.Length -gt $entryRoot.Length) {
                $entryFullPath = $entryFullPath.TrimEnd([char[]]@('\', '/'))
            }
            $isInstallEntry = [string]::Equals(
                $entryFullPath,
                $installFullPath,
                [StringComparison]::OrdinalIgnoreCase
            )
        }
        catch {
            $isInstallEntry = $false
        }
        if ($isInstallEntry) {
            if (-not $processContainsInstall) {
                $processEntries.Add($entry)
                $processContainsInstall = $true
            }
        }
        else {
            $processEntries.Add($entry)
        }
    }
    if (-not $processContainsInstall) {
        $processEntries.Add($installFullPath)
    }
    $env:Path = [string]::Join([string][IO.Path]::PathSeparator, $processEntries.ToArray())
}

function Install-CliRelease {
    param(
        [Parameter(Mandatory = $true)][string]$Tag,
        [Parameter(Mandatory = $true)][string]$InstallDirectory
    )

    Assert-ReleaseTag $Tag
    Assert-AbsoluteInstallDirectory $InstallDirectory
    $architecture = [string][Environment]::GetEnvironmentVariable('PROCESSOR_ARCHITECTURE')
    $wow64Architecture = [string][Environment]::GetEnvironmentVariable('PROCESSOR_ARCHITEW6432')
    Resolve-WindowsCliTarget `
        -IsWindows ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) `
        -Architecture $architecture -Wow64Architecture $wow64Architecture | Out-Null

    $installFullPath = [IO.Path]::GetFullPath($InstallDirectory)
    [void][IO.Directory]::CreateDirectory($installFullPath)
    $targetPath = Join-Path $installFullPath 'confighub.exe'
    $targetItem = Get-Item -LiteralPath $targetPath -Force -ErrorAction SilentlyContinue
    if ($null -ne $targetItem -and
        ($targetItem.PSIsContainer -or ($targetItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
        throw 'Existing CLI target is not a regular file.'
    }

    $version = $Tag.Substring(1)
    $archiveBase = "config-hub-cli_${version}_windows_amd64"
    $archiveName = "$archiveBase.zip"
    $releaseRoot = "$script:ConfigHubWebRoot/releases/download/$Tag"
    $downloadRoot = Join-Path ([IO.Path]::GetTempPath()) `
        ("confighub-cli-install-{0}" -f [Guid]::NewGuid().ToString('N'))
    $stagingPath = Join-Path $installFullPath `
        (".confighub.install.{0}.exe" -f [Guid]::NewGuid().ToString('N'))

    try {
        [void][IO.Directory]::CreateDirectory($downloadRoot)
        $archivePath = Join-Path $downloadRoot $archiveName
        $manifestPath = Join-Path $downloadRoot 'checksums.txt'
        Invoke-DownloadFile -Uri ([Uri]"$releaseRoot/$archiveName") -OutputPath $archivePath
        Invoke-DownloadFile -Uri ([Uri]"$releaseRoot/checksums.txt") -OutputPath $manifestPath
        Assert-ArchiveChecksum -ManifestPath $manifestPath `
            -ArchivePath $archivePath -ArchiveName $archiveName
        Expand-VerifiedCliArchive -ArchivePath $archivePath `
            -ArchiveBase $archiveBase -StagingPath $stagingPath
        Remove-InternetMark -Path $stagingPath
        Assert-CliVersion -Path $stagingPath -ExpectedTag $Tag
        Move-FileAtomically -SourcePath $stagingPath -DestinationPath $targetPath
        Assert-CliVersion -Path $targetPath -ExpectedTag $Tag
        Add-UserPathEntry -InstallDirectory $installFullPath
    }
    finally {
        if ([IO.File]::Exists($stagingPath)) {
            [IO.File]::Delete($stagingPath)
        }
        if ([IO.Directory]::Exists($downloadRoot)) {
            [IO.Directory]::Delete($downloadRoot, $true)
        }
    }

    return "Installed ConfigHub CLI $Tag to $targetPath"
}

function Invoke-InstallCliMain {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RequestedVersion,
        [Parameter(Mandatory = $true)][string]$RequestedInstallDir
    )

    Assert-AbsoluteInstallDirectory $RequestedInstallDir
    $architecture = [string][Environment]::GetEnvironmentVariable('PROCESSOR_ARCHITECTURE')
    $wow64Architecture = [string][Environment]::GetEnvironmentVariable('PROCESSOR_ARCHITEW6432')
    Resolve-WindowsCliTarget -IsWindows ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) `
        -Architecture $architecture -Wow64Architecture $wow64Architecture | Out-Null

    if ([string]::IsNullOrEmpty($RequestedVersion)) {
        $tag = Get-LatestReleaseTag
    }
    else {
        Assert-ReleaseTag $RequestedVersion
        $tag = $RequestedVersion
    }

    Install-CliRelease -Tag $tag -InstallDirectory $RequestedInstallDir
}

if ($MyInvocation.InvocationName -ne '.') {
    Invoke-InstallCliMain -RequestedVersion $Version -RequestedInstallDir $InstallDir
}
