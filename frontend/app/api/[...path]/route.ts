import { NextRequest } from 'next/server';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

// Runtime server-only setting. Never bake access tokens or backend URLs into browser JS.
async function proxy(request: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  const base = process.env.API_INTERNAL_URL || 'http://127.0.0.1:8080';
  const target = new URL(base);
  const { path } = await params;
  target.pathname = '/api/' + path.map(encodeURIComponent).join('/');
  target.search = request.nextUrl.search;
  const headers = new Headers();
  for (const name of ['authorization', 'content-type', 'idempotency-key']) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }
  try {
    const upstream = await fetch(target, {
      method: request.method,
      headers,
      body: ['GET', 'HEAD'].includes(request.method) ? undefined : await request.arrayBuffer(),
      cache: 'no-store',
      redirect: 'manual',
      signal: AbortSignal.timeout(30_000),
    });
    const responseHeaders = new Headers({ 'Cache-Control': 'no-store' });
    const contentType = upstream.headers.get('content-type');
    if (contentType) responseHeaders.set('Content-Type', contentType);
    return new Response(upstream.body, { status: upstream.status, headers: responseHeaders });
  } catch {
    return Response.json({ error: 'Backend unavailable' }, { status: 502 });
  }
}

export { proxy as GET, proxy as POST, proxy as PUT, proxy as DELETE, proxy as PATCH, proxy as HEAD };
