$ErrorActionPreference = 'Stop'
$enc = New-Object System.Text.UTF8Encoding($false)
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

& go build -o smoke_test_gateway.exe .
if ($LASTEXITCODE -ne 0) { throw "go build failed" }

$body1 = '{"model":"gemini-3.8-flash-medium","input":"My name is Metin. Remember it.","stream":false}'
[System.IO.File]::WriteAllText("$env:TEMP\gw_body1.json", $body1, $enc)

$p = Start-Process .\smoke_test_gateway.exe -ArgumentList '-port','8099' -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 3

try {
    $raw1 = curl.exe -s -X POST http://127.0.0.1:8099/v1/responses -H 'Content-Type: application/json' -d "@$env:TEMP\gw_body1.json" --max-time 60
    $j1 = $raw1 | ConvertFrom-Json
    Write-Output ("TUR-1 id: {0} status: {1}" -f $j1.id, $j1.status)

    $body2 = '{"model":"gemini-3.8-flash-medium","input":"What was my name?","stream":false,"previous_response_id":"' + $j1.id + '"}'
    [System.IO.File]::WriteAllText("$env:TEMP\gw_body2.json", $body2, $enc)
    $raw2 = curl.exe -s -X POST http://127.0.0.1:8099/v1/responses -H 'Content-Type: application/json' -d "@$env:TEMP\gw_body2.json" --max-time 60
    $j2 = $raw2 | ConvertFrom-Json
    Write-Output ("TUR-2 id: {0} status: {1}" -f $j2.id, $j2.status)
    Write-Output ("TUR-2 usage: in={0} cache={1} reasoning={2}" -f $j2.usage.input_tokens, $j2.usage.input_tokens_details.cached_tokens, $j2.usage.output_tokens_details.reasoning_tokens)
    $text2 = ($j2.output | Where-Object { $_.type -eq 'message' }).content[0].text
    Write-Output ("TUR-2 yanit: {0}" -f $text2)

    $g1 = (curl.exe -s "http://127.0.0.1:8099/v1/responses/$($j1.id)") | ConvertFrom-Json
    Write-Output ("GET {{id}}: object={0} status={1}" -f $g1.object, $g1.status)

    $d1 = (curl.exe -s -X DELETE "http://127.0.0.1:8099/v1/responses/$($j1.id)") | ConvertFrom-Json
    Write-Output ("DELETE {{id}}: deleted={0}" -f $d1.deleted)

    # TUR-3: response_format + tool_choice + stop + seed — kapalı uç bunları kabul ediyor mu?
    $body3 = '{"model":"gemini-3.8-flash-medium","input":"Say hi in one word.","stream":false,"text":{"format":{"type":"json_object"}},"tools":[{"type":"function","function":{"name":"noop","parameters":{"type":"object"}}}],"tool_choice":"none","stop":["XYZ"],"seed":7}'
    [System.IO.File]::WriteAllText("$env:TEMP\gw_body3.json", $body3, $enc)
    $raw3 = curl.exe -s -X POST http://127.0.0.1:8099/v1/responses -H 'Content-Type: application/json' -d "@$env:TEMP\gw_body3.json" --max-time 60
    Write-Output ("TUR-3 (response_format+tool_choice+stop+seed): {0}" -f $raw3.Substring(0, [Math]::Min(260, $raw3.Length)))

    # TUR-4: stream — reasoning gercekten anlik (delta delta) akiyor mu?
    $body4 = '{"model":"gemini-3.8-flash-high","input":"Why is the sky blue? Think first.","stream":true}'
    [System.IO.File]::WriteAllText("$env:TEMP\gw_body4.json", $body4, $enc)
    $raw4 = curl.exe -s -N -X POST http://127.0.0.1:8099/v1/responses -H 'Content-Type: application/json' -d "@$env:TEMP\gw_body4.json" --max-time 90
    $events = @{}
    foreach ($line in $raw4) {
        if ($line.StartsWith('event: ')) {
            $e = $line.Substring(7).Trim()
            if ($events.ContainsKey($e)) { $events[$e]++ } else { $events[$e] = 1 }
        }
    }
    $summary = ($events.GetEnumerator() | Sort-Object Name | ForEach-Object { "{0}x{1}" -f $_.Name, $_.Value }) -join " | "
    Write-Output ("TUR-4 (stream) olay dagilimi: {0}" -f $summary)

    # TUR-5: zamanlama — delta'lar ayni hizda geciyor mu? (sonda birikme yok mu?)
    $body5 = '{"model":"gemini-3.8-flash-high","input":"Count from 1 to 25 slowly. Think briefly about each number first.","stream":true}'
    [System.IO.File]::WriteAllText("$env:TEMP\gw_body5.json", $body5, $enc)
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $ts = New-Object System.Collections.Generic.List[double]
    curl.exe -s -N -X POST http://127.0.0.1:8099/v1/responses -H 'Content-Type: application/json' -d "@$env:TEMP\gw_body5.json" --max-time 120 | ForEach-Object {
        if ($_ -like 'event: response.*delta') { $ts.Add($sw.Elapsed.TotalMilliseconds) }
    }
    if ($ts.Count -ge 3) {
        $gaps = @()
        for ($i = 1; $i -lt $ts.Count; $i++) { $gaps += ($ts[$i] - $ts[$i-1]) }
        $avg = ($gaps | Measure-Object -Average).Average
        $maxGap = ($gaps | Measure-Object -Maximum).Maximum
        Write-Output ("TUR-5 (timing): {0} delta | ilk->son {1:N0} ms | ort. bosluk {2:N0} ms | max bosluk {3:N0} ms" -f $ts.Count, ($ts[$ts.Count-1] - $ts[0]), $avg, $maxGap)
    } else {
        Write-Output ("TUR-5 (timing): yetersiz delta ({0})" -f $ts.Count)
    }
} finally {
    Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
    Remove-Item .\smoke_test_gateway.exe -ErrorAction SilentlyContinue
}