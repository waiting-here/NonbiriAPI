import { afterEach, describe, expect, it, vi } from 'vitest';
import { gameRequest } from './request';

describe('bounded game transport', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('rejects a streamed response as soon as it crosses the game response limit', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('x'.repeat(256 * 1024 + 1), { status: 200 })),
    );
    await expect(gameRequest('/api/games/test')).rejects.toMatchObject({
      code: 'invalid_response',
      status: 200,
    });
  });

  it('rejects malformed UTF-8 before JSON parsing', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(Uint8Array.of(0xc3, 0x28), { status: 200 })),
    );
    await expect(gameRequest('/api/games/test')).rejects.toMatchObject({
      code: 'invalid_response',
      status: 200,
    });
  });

  it('accepts an explicitly bounded catalog while preserving the smaller default and error limits',async()=>{
    const body=JSON.stringify({value:'x'.repeat(300*1024)});
    vi.stubGlobal('fetch',vi.fn(async()=>new Response(body,{status:200})));
    await expect(gameRequest('/api/games/likes/catalog',{maxResponseBytes:1024*1024})).resolves.toMatchObject({status:200});
    await expect(gameRequest('/api/games/likes/catalog')).rejects.toMatchObject({code:'invalid_response'});
    vi.stubGlobal('fetch',vi.fn(async()=>new Response(body,{status:500})));
    await expect(gameRequest('/api/games/likes/catalog',{maxResponseBytes:1024*1024})).rejects.toMatchObject({code:'invalid_response'});
  });

  it('rejects unsafe or unbounded response-budget options before fetching',async()=>{
    const fetch=vi.fn();vi.stubGlobal('fetch',fetch);
    for(const maxResponseBytes of [0,Infinity,NaN,8*1024*1024+1]) await expect(gameRequest('/api/games/test',{maxResponseBytes})).rejects.toMatchObject({code:'invalid_request'});
    expect(fetch).not.toHaveBeenCalled();
  });
});
