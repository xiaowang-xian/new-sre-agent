import os
import uuid
from pathlib import Path
from typing import List

import chromadb
import requests
from flask import Flask, jsonify, request
from sentence_transformers import SentenceTransformer

DATA_DIR = Path(os.getenv("DATA_DIR", "/data"))
CHROMA_DIR = os.getenv("CHROMA_DIR", str(DATA_DIR / "chroma"))
COLLECTION_NAME = os.getenv("COLLECTION_NAME", "sre_knowledge")
EMBED_MODEL = os.getenv("EMBED_MODEL", "sentence-transformers/all-MiniLM-L6-v2")
OLLAMA_URL = os.getenv("OLLAMA_URL", "http://ollama-service.ai-services.svc.cluster.local:11434/api/generate")
OLLAMA_MODEL = os.getenv("OLLAMA_MODEL", "qwen2:7b-instruct")

app = Flask(__name__)
embedder = SentenceTransformer(EMBED_MODEL)
client = chromadb.PersistentClient(path=CHROMA_DIR)
collection = client.get_or_create_collection(name=COLLECTION_NAME)


def split_text(text: str, chunk_size: int = 600, overlap: int = 80) -> List[str]:
    text = text.replace("\r\n", "\n")
    chunks = []
    start = 0
    while start < len(text):
        end = min(start + chunk_size, len(text))
        chunks.append(text[start:end])
        if end == len(text):
            break
        start = max(0, end - overlap)
    return [c.strip() for c in chunks if c.strip()]


def embed(texts: List[str]):
    return embedder.encode(texts, normalize_embeddings=True).tolist()


@app.get("/healthz")
def healthz():
    return jsonify({"status": "ok", "collection_count": collection.count()})


@app.post("/load_knowledge")
def load_knowledge():
    body = request.get_json(force=True)
    path = Path(body.get("path", DATA_DIR / "sre_manual.txt"))
    if not path.exists():
        return jsonify({"status": "error", "message": f"file not found: {path}"}), 404
    text = path.read_text(encoding="utf-8")
    chunks = split_text(text)
    ids = [str(uuid.uuid4()) for _ in chunks]
    metadatas = [{"source": str(path), "chunk": i} for i, _ in enumerate(chunks)]
    collection.add(ids=ids, documents=chunks, embeddings=embed(chunks), metadatas=metadatas)
    return jsonify({"status": "success", "count": len(chunks), "path": str(path)})


@app.post("/retrieve")
def retrieve():
    body = request.get_json(force=True)
    query = body.get("query", "")
    top_k = int(body.get("top_k", 3))
    if not query:
        return jsonify({"query": query, "context": "", "sources": []})
    result = collection.query(query_embeddings=embed([query]), n_results=top_k)
    docs = result.get("documents", [[]])[0]
    metas = result.get("metadatas", [[]])[0]
    sources = [f"{m.get('source')}#chunk={m.get('chunk')}" for m in metas]
    return jsonify({"query": query, "context": "\n---\n".join(docs), "sources": sources})


@app.post("/rag_query")
def rag_query():
    body = request.get_json(force=True)
    query = body.get("query", "")
    top_k = int(body.get("top_k", 3))
    rr = collection.query(query_embeddings=embed([query]), n_results=top_k)
    docs = rr.get("documents", [[]])[0]
    context = "\n---\n".join(docs)
    prompt = f"""你是资深 K8s SRE，请只基于知识库回答。\n知识库：\n{context}\n\n问题：{query}\n"""
    resp = requests.post(OLLAMA_URL, json={"model": OLLAMA_MODEL, "prompt": prompt, "stream": False}, timeout=60)
    resp.raise_for_status()
    return jsonify({"query": query, "context": context, "response": resp.json().get("response", "")})


if __name__ == "__main__":
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    app.run(host="0.0.0.0", port=8000)
