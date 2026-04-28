<template>
  <div class="page">
    <header>
      <h1>K8s SRE 智能运维助手</h1>
      <p>告警接入、AI 决策、自愈执行、日志检索与面试演示面板</p>
      <button @click="loadAll">刷新数据</button>
    </header>

    <section class="cards">
      <div class="card"><span>故障总数</span><b>{{ stats.total || 0 }}</b></div>
      <div class="card"><span>成功处理</span><b>{{ stats.success || 0 }}</b></div>
      <div class="card"><span>处理失败</span><b>{{ stats.failed || 0 }}</b></div>
      <div class="card"><span>处理中</span><b>{{ stats.running || 0 }}</b></div>
    </section>

    <section class="grid">
      <div class="panel">
        <h2>故障历史</h2>
        <table>
          <thead><tr><th>时间</th><th>故障</th><th>对象</th><th>动作</th><th>状态</th></tr></thead>
          <tbody>
            <tr v-for="f in faults" :key="f.id">
              <td>{{ fmt(f.started_at) }}</td><td>{{ f.alert_name }}</td><td>{{ f.namespace }}/{{ f.name }}</td><td>{{ f.action }}</td><td>{{ f.status }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="panel">
        <h2>节点状态</h2>
        <table>
          <thead><tr><th>节点</th><th>Ready</th><th>不可调度</th></tr></thead>
          <tbody><tr v-for="n in nodes" :key="n.name"><td>{{ n.name }}</td><td>{{ n.ready }}</td><td>{{ n.unschedulable }}</td></tr></tbody>
        </table>
      </div>
    </section>

    <section class="panel">
      <h2>Agent 日志</h2>
      <input v-model="keyword" placeholder="输入关键词搜索日志，如 RESTART_POD" @keyup.enter="loadLogs" />
      <button @click="loadLogs">搜索</button>
      <pre v-for="(l, i) in logs" :key="i">{{ l }}</pre>
    </section>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import axios from 'axios'
const API = import.meta.env.VITE_API_BASE || '/api'
const stats = ref({})
const faults = ref([])
const nodes = ref([])
const logs = ref([])
const keyword = ref('')
const fmt = (v) => v ? new Date(v).toLocaleString() : '-'
async function loadAll(){
  stats.value = (await axios.get(`${API}/stats/`)).data
  faults.value = (await axios.get(`${API}/faults/`)).data
  nodes.value = (await axios.get(`${API}/nodes/`)).data.items || []
  await loadLogs()
}
async function loadLogs(){
  logs.value = (await axios.get(`${API}/logs/`, { params: { q: keyword.value }})).data.items || []
}
onMounted(loadAll)
</script>
