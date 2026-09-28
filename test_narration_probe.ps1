$ErrorActionPreference = 'Stop'
$enc = New-Object System.Text.UTF8Encoding($false)
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

# ARA YANIT (narration) PROBESI
# Soru: Gateway, modelin arac cagirmadan once yazdigi GORUNUR ara metni dusuruyor mu?
# Varsayim: hayir — model davranisi farki. Test: gorunur metin isteyip arac cagirt.

function Invoke-NarrationProbe($model) {
    $body = @{
        model = $model
        input = 'Test suresi. Simdi my_tool adindaki araci cagiracaksin. AMA KURAL: arac cagirmadan ONCE kullaniciya gorunur bir ara yanit yaz (tek cumle, ornek: Simdi araci cagiriyorum). Bu metin gorunur cevap olmali, dusunce kanalina yazilmamali. Sonra my_tool aracini note parametresiyle cagir.'
        tools = @(@{
            type = 'function'
            function = @{
                name = 'my_tool'
                description = 'Test araci'
                parameters = @{
                    type = 'object'
                    properties = @{ note = @{ type = 'string' } }
                    required = @('note')
                }
            }
        })
        stream = $false
    } | ConvertTo-Json -Compress -Depth 10
    [System.IO.File]::WriteAllText("$env:TEMP\gw_narr.json", $body, $enc)
    $raw = curl.exe -s -X POST http://127.0.0.1:8000/v1/responses -H 'Content-Type: application/json' -d "@$env:TEMP\gw_narr.json" --max-time 90
    $j = $raw | ConvertFrom-Json
    Write-Output ("=== MODEL: {0} | status: {1} ===" -f $model, $j.status)
    foreach ($o in $j.output) {
        if ($o.type -eq 'message') {
            Write-Output ("  [GORUNUR METIN] {0}" -f $o.content[0].text)
        } elseif ($o.type -eq 'reasoning') {
            $t = $o.content[0].text
            if ($t.Length -gt 160) { $t = $t.Substring(0, 160) + '...' }
            Write-Output ("  [DUSUNCE] {0}" -f $t)
        } elseif ($o.type -eq 'function_call') {
            Write-Output ("  [ARAC CAGRISI] {0} args={1}" -f $o.name, $o.arguments)
        } else {
            Write-Output ("  [{0}]" -f $o.type)
        }
    }
    $types = ($j.output | ForEach-Object { $_.type }) -join ','
    Write-Output ("  -> output tipleri: {0}" -f $types)
    Write-Output ""
}

Invoke-NarrationProbe 'gemini-3.8-flash-high'
Invoke-NarrationProbe 'gemini-3.8-flash-nothink'