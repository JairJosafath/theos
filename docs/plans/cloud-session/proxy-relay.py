# Relays the session's HTTPS proxy (127.0.0.1:38377) onto the Kind network's
# gateway, so Kind nodes and pods reach the internet only through that proxy.
import asyncio, sys
LISTEN_HOST, LISTEN_PORT = sys.argv[1], int(sys.argv[2])
UP_HOST, UP_PORT = "127.0.0.1", int(sys.argv[3])

async def pipe(r, w):
    try:
        while data := await r.read(65536):
            w.write(data)
            await w.drain()
    except Exception:
        pass
    finally:
        try:
            w.close()
        except Exception:
            pass

async def handle(cr, cw):
    try:
        ur, uw = await asyncio.open_connection(UP_HOST, UP_PORT)
    except Exception:
        cw.close()
        return
    await asyncio.gather(pipe(cr, uw), pipe(ur, cw))

async def main():
    srv = await asyncio.start_server(handle, LISTEN_HOST, LISTEN_PORT)
    async with srv:
        await srv.serve_forever()

asyncio.run(main())
