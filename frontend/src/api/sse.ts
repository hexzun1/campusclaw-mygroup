// Zero-dependency SSE frame parser. The stream is read with fetch (the bearer
// token must ride along), so frames arrive here as byte chunks: TextDecoder
// decodes with stream:true (a multi-byte character split across chunks
// survives), frames are delimited by blank lines, and \n, \r\n and a lone \r
// all terminate a line.

export interface SSEEvent {
  event: string
  data: string
}

export interface SSEParser {
  feed(chunk: Uint8Array): void
  // Flush the decoder and treat whatever remains as the final line. The
  // server always ends its last frame with a blank line, so this only
  // matters for a truncated stream.
  end(): void
}

export function createSSEParser(onEvent: (event: SSEEvent) => void): SSEParser {
  const decoder = new TextDecoder()
  let buffer = ''
  let eventName = ''
  let dataLines: string[] = []

  function dispatchFrame() {
    if (dataLines.length > 0) {
      onEvent({ event: eventName || 'message', data: dataLines.join('\n') })
    }
    eventName = ''
    dataLines = []
  }

  function handleLine(line: string) {
    if (line === '') {
      dispatchFrame()
      return
    }
    if (line.startsWith(':')) return
    const colon = line.indexOf(':')
    const field = colon === -1 ? line : line.slice(0, colon)
    let value = colon === -1 ? '' : line.slice(colon + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') eventName = value
    else if (field === 'data') dataLines.push(value)
  }

  function drain(flush: boolean) {
    let lineStart = 0
    let pos = 0
    while (pos < buffer.length) {
      const ch = buffer[pos]
      if (ch === '\n') {
        handleLine(buffer.slice(lineStart, pos))
        pos += 1
        lineStart = pos
      } else if (ch === '\r') {
        // The \r may be the first half of a \r\n pair split across chunks:
        // hold it until the next chunk (or the end) decides.
        if (pos + 1 === buffer.length && !flush) break
        handleLine(buffer.slice(lineStart, pos))
        pos += 1
        if (pos < buffer.length && buffer[pos] === '\n') pos += 1
        lineStart = pos
      } else {
        pos += 1
      }
    }
    buffer = buffer.slice(lineStart)
    if (flush) {
      if (buffer !== '') {
        handleLine(buffer)
        buffer = ''
      }
      dispatchFrame()
    }
  }

  return {
    feed(chunk: Uint8Array) {
      buffer += decoder.decode(chunk, { stream: true })
      drain(false)
    },
    end() {
      buffer += decoder.decode()
      drain(true)
    },
  }
}
