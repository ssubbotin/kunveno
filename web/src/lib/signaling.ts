import { parseServerMessage, type ClientMessage, type ServerMessage } from './protocol';

export class Signaling {
  private ws: WebSocket | null = null;

  constructor(
    private readonly url: string,
    private readonly onMessage: (m: ServerMessage) => void,
    private readonly onClose: () => void,
  ) {}

  connect(): Promise<void> {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(this.url);
      ws.onopen = () => {
        this.ws = ws;
        resolve();
      };
      ws.onerror = () => reject(new Error('websocket error'));
      ws.onmessage = (ev) => {
        try {
          this.onMessage(parseServerMessage(String(ev.data)));
        } catch (e) {
          console.error(e);
        }
      };
      ws.onclose = () => {
        this.ws = null;
        this.onClose();
      };
    });
  }

  send(msg: ClientMessage): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg));
  }

  close(): void {
    const ws = this.ws;
    this.ws = null;
    ws?.close();
  }
}
