FROM node:22-alpine

WORKDIR /app

# Install dependencies first to leverage Docker layer caching.
COPY package.json ./
RUN npm install --omit=dev

COPY index.js ./

EXPOSE 3000

HEALTHCHECK --interval=2s --timeout=3s --retries=15 \
  CMD wget -qO- "http://localhost:3000/socket.io/?EIO=4&transport=polling" >/dev/null 2>&1 || exit 1

CMD ["node", "index.js"]
