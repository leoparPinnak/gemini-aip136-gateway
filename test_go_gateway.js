const http = require('http');

function post(urlPath, body, onChunk, onEnd, onError) {
    const data = JSON.stringify(body);
    const req = http.request({
        hostname: '127.0.0.1',
        port: 3060,
        path: urlPath,
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'Content-Length': Buffer.byteLength(data)
        }
    }, (res) => {
        let buf = '';
        res.on('data', chunk => {
            buf += chunk.toString();
            if (onChunk) onChunk(chunk.toString());
        });
        res.on('end', () => {
            if (onEnd) onEnd(buf, res.statusCode);
        });
    });

    req.on('error', onError);
    req.write(data);
    req.end();
}

function get(urlPath, callback) {
    http.get({
        hostname: '127.0.0.1',
        port: 3060,
        path: urlPath
    }, (res) => {
        let buf = '';
        res.on('data', c => buf += c);
        res.on('end', () => {
            try {
                callback(null, JSON.parse(buf), res.statusCode);
            } catch (e) {
                callback(e, buf, res.statusCode);
            }
        });
    }).on('error', callback);
}

async function runTests() {
    console.log('='.repeat(70));
    console.log('🧪 TEST 1: GET /health ve GET /v1/models (Go Gateway Port 3060)');
    console.log('='.repeat(70));

    await new Promise((resolve) => {
        get('/health', (err, data) => {
            if (err) console.error('Health test error:', err);
            else console.log('[✓] Health OK:', data.status, '| Gateway:', data.name, '| TLS:', data.engine);
            resolve();
        });
    });

    await new Promise((resolve) => {
        get('/v1/models', (err, data) => {
            if (err) console.error('Models test error:', err);
            else console.log('[✓] Models count:', data.data?.length, '| Models:', data.data?.map(m => m.id).join(', '));
            resolve();
        });
    });

    console.log('\n' + '='.repeat(70));
    console.log('🧪 TEST 2: POST /v1/responses (OpenAI Responses API Streaming - Go)');
    console.log('='.repeat(70));

    await new Promise((resolve) => {
        let sseEvents = [];
        let modelText = '';
        let thoughtText = '';

        post('/v1/responses', {
            model: 'deepseek-v4-flash',
            input: [
                {
                    role: 'user',
                    content: 'Selam! Go ile yazılmış yeni gateway üzerinden cevap veriyorsun. Tek kısa bir cümleyle selam ver.'
                }
            ],
            stream: true,
            reasoning: { effort: 'low' }
        }, (chunk) => {
            const lines = chunk.split('\n');
            for (const l of lines) {
                if (l.startsWith('event: ')) {
                    sseEvents.push(l.slice(7).trim());
                }
                if (l.startsWith('data: ')) {
                    try {
                        const d = JSON.parse(l.slice(6));
                        if (d.delta && d.type === 'response.reasoning_text.delta') thoughtText += d.delta;
                        if (d.delta && d.type === 'response.output_text.delta') modelText += d.delta;
                        if (d.type === 'response.completed') {
                            console.log('[✓] response.completed event received!');
                            console.log('    Usage:', JSON.stringify(d.response?.usage));
                        }
                    } catch (e) {}
                }
            }
        }, (full, status) => {
            console.log(`[✓] Responses API HTTP ${status}`);
            console.log('    Unique Events:', Array.from(new Set(sseEvents)).join(', '));
            if (thoughtText) console.log('    Thought snippet:', thoughtText.slice(0, 100).replace(/\n/g, ' '));
            console.log('    Model Output:', modelText.trim());
            resolve();
        }, (err) => {
            console.error('Responses API error:', err);
            resolve();
        });
    });

    console.log('\n' + '='.repeat(70));
    console.log('🧪 TEST 3: POST /v1/chat/completions (Tool Calling Streaming - Go)');
    console.log('='.repeat(70));

    await new Promise((resolve) => {
        let toolCallDetected = false;
        let toolName = '';
        let toolArgs = '';

        post('/v1/chat/completions', {
            model: 'deepseek-chat',
            messages: [
                {
                    role: 'user',
                    content: 'C:\\Users\\metin\\Desktop klasöründeki dosyaları listelemek için run_command aracını kullan.'
                }
            ],
            tools: [
                {
                    type: 'function',
                    function: {
                        name: 'run_command',
                        description: 'PowerShell veya terminal komutları çalıştırır.',
                        parameters: {
                            type: 'object',
                            properties: {
                                command: { type: 'string', description: 'Çalıştırılacak terminal komutu' }
                            },
                            required: ['command']
                        }
                    }
                }
            ],
            stream: true
        }, (chunk) => {
            const lines = chunk.split('\n');
            for (const l of lines) {
                if (l.startsWith('data: ') && !l.includes('[DONE]')) {
                    try {
                        const d = JSON.parse(l.slice(6));
                        const tc = d.choices?.[0]?.delta?.tool_calls?.[0];
                        if (tc) {
                            toolCallDetected = true;
                            if (tc.function?.name) toolName = tc.function.name;
                            if (tc.function?.arguments) toolArgs += tc.function.arguments;
                        }
                    } catch (e) {}
                }
            }
        }, (full, status) => {
            console.log(`[✓] Chat Completions Tool Call HTTP ${status}`);
            console.log('    Tool Call Detected:', toolCallDetected);
            console.log('    Tool Name:', toolName);
            console.log('    Tool Arguments:', toolArgs.trim());
            resolve();
        }, (err) => {
            console.error('Chat Completions error:', err);
            resolve();
        });
    });

    console.log('\n' + '='.repeat(70));
    console.log('🎉 TÜM GO GATEWAY TESTLERİ TAMAMLANDI!');
    console.log('='.repeat(70));
}

runTests();
