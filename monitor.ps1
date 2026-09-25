# monitor.ps1 — cache-hit / token izleme paneli (Faz 0-1 JSONL logu)
# Kullanım:  powershell -File monitor.ps1            -> genel panel
#            powershell -File monitor.ps1 -Since 16:42:47 -> sadece o saatten sonra
#            powershell -File monitor.ps1 -Json      -> ham özet (makine okunur)

param(
    [string]$Since = "",
    [switch]$Json
)

$ErrorActionPreference = "SilentlyContinue"
Set-Location $PSScriptRoot

$f = Get-ChildItem logs\requests-*.jsonl | Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not $f) { Write-Output "LOG YOK: logs/requests-*.jsonl"; exit 1 }

$rows = Get-Content $f.FullName | ForEach-Object { try { $_ | ConvertFrom-Json } catch {} }
$reqs = @($rows | Where-Object { $_.req_id })
$evts = @($rows | Where-Object { -not $_.req_id })
if ($Since -ne "") { $reqs = @($reqs | Where-Object { $_.ts.Substring(11,8) -ge $Since }); $evts = @($evts | Where-Object { $_.ts.Substring(11,8) -ge $Since }) }

$cacheable = @($reqs | Where-Object { $_.prompt -gt 4096 })
$hits      = @($cacheable | Where-Object { $_.cached -gt 0 })
$misses    = @($cacheable | Where-Object { $_.cached -eq 0 })
$fail      = @($reqs | Where-Object { $_.status -ne "completed" })
$reused    = @($reqs | Where-Object { $_.conn_reused -eq $true })

$hitPct   = if ($cacheable.Count)  { [math]::Round(100*$hits.Count/$cacheable.Count,1) } else { 0 }
$missPct  = [math]::Round(100 - $hitPct,1)
$cachedTok = ($hits | Measure-Object -Property cached -Sum).Sum
$sumPrompt = ($reqs | Measure-Object -Property prompt -Sum).Sum

$summary = [ordered]@{
    dosya             = $f.Name
    son_yazim         = $f.LastWriteTime.ToString("yyyy-MM-dd HH:mm:ss")
    istek             = $reqs.Count
    completed         = @($reqs | Where-Object { $_.status -eq "completed" }).Count
    hata_truncated    = $fail.Count
    cachelenebilir    = $cacheable.Count
    hit               = $hits.Count
    miss              = $misses.Count
    hit_yuzde         = $hitPct
    miss_yuzde        = $missPct          # hedef: < 2
    hit_olen_token    = $cachedTok
    toplam_prompt     = $sumPrompt
    conn_reused       = $reused.Count
    olay_sayisi       = $evts.Count
    olay_dagilimi     = (($evts | ForEach-Object { $_.Kind } | Group-Object | ForEach-Object { "$($_.Name)=$($_.Count)" }) -join ", ")
}

if ($Json) { $summary | ConvertTo-Json -Depth 3; exit 0 }

Write-Output "=== CACHE-HIT IZLEME PANELI  ($($summary.son_yazim)) ==="
Write-Output ("istek: {0} (completed {1}, hata/koptu {2})   olay: {3}" -f $summary.istek, $summary.completed, $summary.hata_truncated, $summary.olay_sayisi)
Write-Output ("cache'lenebilir: {0} -> HIT {1} / MISS {2}   hit %{3}  MISS %{4}  [hedef <2]" -f $cacheable.Count, $hits.Count, $misses.Count, $hitPct, $missPct)
Write-Output ("hit ile tasinan token: {0} / toplam prompt {1} ({2}%)" -f $cachedTok, $sumPrompt, $(if ($sumPrompt) { [math]::Round(100*$cachedTok/$sumPrompt,1) } else { 0 }))
Write-Output ("conn_reused: {0}/{1}   olaylar: {2}" -f $reused.Count, $reqs.Count, $summary.olay_dagilimi)
if ($misses.Count) {
    Write-Output "--- MISS listesi (kok-neden icin son 15) ---"
    $misses | Select-Object -Last 15 | ForEach-Object {
        Write-Output ("  {0} pr={1,-6} ep={2,-22} sys={3} tools={4} trace={5}" -f $_.ts.Substring(11,8), $_.prompt, $_.endpoint, $_.sys_hash, $_.tools_hash, $_.up_trace)
    }
}
