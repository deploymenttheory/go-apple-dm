# Summarizes the samples recorded by sample-resources.ps1 after a failed test
# step: the dynamic port range, the peak socket counts and the lowest available
# memory, each with the test binaries running at that moment.
param(
    [Parameter(Mandatory)][string]$Path
)

netsh int ipv4 show dynamicport tcp
netsh int ipv4 show excludedportrange protocol=tcp

$lines = @()
if (Test-Path $Path) {
    $lines = @(Get-Content $Path | Where-Object { $_ -match '\btotal=\d+' })
}
if ($lines.Count -eq 0) {
    'No resource samples were recorded.'
    return
}

function Get-SampleValue([string]$Line, [string]$Name) {
    if ($Line -match "\b$Name=(\d+)") {
        return [int]$Matches[1]
    }
    return 0
}

"Resource samples: $($lines.Count), from $($lines[0].Split(' ')[0]) to $($lines[-1].Split(' ')[0])"
foreach ($name in 'dynamic', 'TimeWait', 'Established', 'Bound', 'nonpagedMB') {
    $peak = $lines | Sort-Object { Get-SampleValue $_ $name } -Descending | Select-Object -First 1
    "Peak ${name}: $peak"
}
$lowest = $lines | Sort-Object { Get-SampleValue $_ 'availableMB' } | Select-Object -First 1
"Lowest availableMB: $lowest"
