# Records the runner's TCP connections and kernel memory every few seconds while
# the Windows unit tests run, so a failure to open a socket can be classified as
# port exhaustion, memory pressure or neither. Stops when StopPath exists.
param(
    [Parameter(Mandatory)][string]$Path,
    [Parameter(Mandatory)][string]$StopPath,
    [int]$IntervalSeconds = 5
)

$startPort = 49152
$range = netsh int ipv4 show dynamicport tcp | Select-String 'Start Port\s*:\s*(\d+)'
if ($range) {
    $startPort = [int]$range.Matches[0].Groups[1].Value
}

while (-not (Test-Path $StopPath)) {
    try {
        $connections = @(Get-NetTCPConnection -ErrorAction Stop)
        $states = ($connections | Group-Object State | Sort-Object Name | ForEach-Object { "$($_.Name)=$($_.Count)" }) -join ' '
        $dynamic = @($connections | Where-Object { $_.LocalPort -ge $startPort -and $_.State -ne 'Listen' }).Count
        $memory = Get-CimInstance Win32_PerfFormattedData_PerfOS_Memory -ErrorAction Stop
        $tests = (@(Get-Process -ErrorAction SilentlyContinue | Where-Object ProcessName -Like '*.test' | ForEach-Object ProcessName) | Sort-Object -Unique) -join ','
        $line = '{0:HH:mm:ss} total={1} dynamic={2} {3} availableMB={4} nonpagedMB={5} tests={6}' -f (Get-Date), $connections.Count, $dynamic, $states, $memory.AvailableMBytes, [math]::Round($memory.PoolNonpagedBytes / 1MB), $tests
    } catch {
        $line = '{0:HH:mm:ss} sample failed: {1}' -f (Get-Date), ($_.Exception.Message -replace '\s+', ' ')
    }
    Add-Content -Path $Path -Value $line
    Start-Sleep -Seconds $IntervalSeconds
}
