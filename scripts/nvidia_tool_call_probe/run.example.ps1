# nvidia_tool_call_probe one-click run example (PowerShell)
# Working directory: repo root (antigravity-proxy-desktop-go)
# Usage: powershell -ExecutionPolicy Bypass -File scripts/nvidia_tool_call_probe/run.example.ps1
# NOTE: This file is intentionally ASCII-only so Windows PowerShell 5.1 (GBK locale)
#       parses it correctly without requiring a UTF-8 BOM.

param(
    [string]$Models   = "moonshotai/kimi-k3",
    [string]$Token    = "",
    [string]$Prompt   = "",
    [switch]$Parallel
)

$ErrorActionPreference = "Stop"

$goArgs = @("run", "./scripts/nvidia_tool_call_probe",
    "-models", $Models
)
if ($Token)    { $goArgs += @("-token", $Token) }
if ($Prompt)   { $goArgs += @("-prompt", $Prompt) }   # empty -> probe uses built-in default prompt
if ($Parallel) { $goArgs += "-parallel" }

Write-Host "[nvidia_tool_call_probe] launch: $($goArgs -join ' ')"
& go @goArgs
