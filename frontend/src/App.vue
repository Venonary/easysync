<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'

const STORAGE_DEVICE_ID = 'easysync.deviceId'
const STORAGE_DEVICE_NAME = 'easysync.deviceName'
const STORAGE_ACTIVE_CONVERSATION = 'easysync.activeConversationId'

const state = reactive({
  conversations: [],
  messages: [],
  presence: [],
  connected: false,
  error: ''
})

const text = ref('')
const activeConversationId = ref(localStorage.getItem(STORAGE_ACTIVE_CONVERSATION) || '')
const deviceId = getOrCreateDeviceId()
const deviceName = ref(localStorage.getItem(STORAGE_DEVICE_NAME) || defaultDeviceName())
const socket = ref(null)
const messageList = ref(null)
const editingMessageId = ref('')
const editingText = ref('')

const activeConversation = computed(() => {
  return state.conversations.find((item) => item.id === activeConversationId.value) || state.conversations[0]
})

const visibleMessages = computed(() => {
  const id = activeConversation.value?.id
  if (!id) return []
  return state.messages.filter((message) => message.conversationId === id)
})

const groupedPresence = computed(() => {
  const seen = new Map()
  for (const item of state.presence) {
    const key = item.id || item.socket
    if (!seen.has(key)) seen.set(key, item)
  }
  return [...seen.values()]
})

watch(deviceName, (value) => {
  localStorage.setItem(STORAGE_DEVICE_NAME, value.trim() || defaultDeviceName())
})

watch(activeConversationId, (value) => {
  if (value) localStorage.setItem(STORAGE_ACTIVE_CONVERSATION, value)
})

watch(visibleMessages, async () => {
  await nextTick()
  if (messageList.value) {
    messageList.value.scrollTop = messageList.value.scrollHeight
  }
})

onMounted(async () => {
  await loadState()
  connect()
})

onBeforeUnmount(() => {
  socket.value?.close()
})

async function loadState() {
  const response = await fetch('/api/state')
  if (!response.ok) throw new Error('无法读取服务端状态')
  const data = await response.json()
  state.conversations = data.conversations || []
  state.messages = data.messages || []
  if (!activeConversationId.value || !state.conversations.some((item) => item.id === activeConversationId.value)) {
    activeConversationId.value = state.conversations[0]?.id || ''
  }
}

function connect() {
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const params = new URLSearchParams({
    deviceId,
    deviceName: deviceName.value,
    deviceType: platformName()
  })
  const ws = new WebSocket(`${protocol}//${location.host}/ws?${params.toString()}`)
  socket.value = ws

  ws.addEventListener('open', () => {
    state.connected = true
    state.error = ''
  })
  ws.addEventListener('close', () => {
    state.connected = false
    setTimeout(connect, 1500)
  })
  ws.addEventListener('error', () => {
    state.error = '实时连接异常，正在重连'
  })
  ws.addEventListener('message', (event) => {
    const payload = JSON.parse(event.data)
    handleServerEvent(payload)
  })
}

function handleServerEvent(event) {
  if (event.type === 'message') {
    upsertMessage(event.data)
  } else if (event.type === 'message_updated') {
    upsertMessage(event.data)
  } else if (event.type === 'message_deleted') {
    removeMessageById(event.data.id)
  } else if (event.type === 'conversations') {
    state.conversations = event.data
    if (!state.conversations.some((item) => item.id === activeConversationId.value)) {
      activeConversationId.value = state.conversations[0]?.id || ''
    }
  } else if (event.type === 'conversation_created') {
    upsertConversation(event.data)
  } else if (event.type === 'conversation_updated') {
    upsertConversation(event.data)
  } else if (event.type === 'conversation_deleted') {
    state.conversations = state.conversations.filter((item) => item.id !== event.data.id)
    state.messages = state.messages.filter((item) => item.conversationId !== event.data.id)
  } else if (event.type === 'presence') {
    state.presence = event.data || []
  } else if (event.type === 'error') {
    state.error = event.data
  }
}

function upsertConversation(conversation) {
  const index = state.conversations.findIndex((item) => item.id === conversation.id)
  if (index >= 0) state.conversations[index] = conversation
  else state.conversations.unshift(conversation)
}

function upsertMessage(message) {
  const index = state.messages.findIndex((item) => item.id === message.id)
  if (index >= 0) state.messages[index] = message
  else state.messages.push(message)
}

function removeMessageById(messageId) {
  state.messages = state.messages.filter((item) => item.id !== messageId)
  if (editingMessageId.value === messageId) {
    editingMessageId.value = ''
    editingText.value = ''
  }
}

function sendMessage() {
  const content = text.value.trim()
  if (!content || !activeConversation.value || socket.value?.readyState !== WebSocket.OPEN) return
  socket.value.send(JSON.stringify({
    type: 'send_message',
    conversationId: activeConversation.value.id,
    messageType: 'text',
    content,
    deviceId,
    deviceName: deviceName.value,
    deviceType: platformName()
  }))
  text.value = ''
}

async function pasteAndSend() {
  if (!navigator.clipboard?.readText) {
    state.error = '当前浏览器不支持直接读取剪贴板，请手动粘贴'
    return
  }
  try {
    const value = await navigator.clipboard.readText()
    text.value = value
    sendMessage()
  } catch {
    state.error = '浏览器拒绝读取剪贴板，可尝试 HTTPS 或手动粘贴'
  }
}

async function copyMessage(content) {
  try {
    await navigator.clipboard.writeText(content)
  } catch {
    state.error = '复制失败，请手动选择文本复制'
  }
}

function startEditMessage(message) {
  if (!isMine(message)) return
  editingMessageId.value = message.id
  editingText.value = message.content
}

function cancelEditMessage() {
  editingMessageId.value = ''
  editingText.value = ''
}

async function saveEditMessage(message) {
  const content = editingText.value.trim()
  if (!content) {
    state.error = '消息内容不能为空'
    return
  }
  const response = await fetch(`/api/messages/${message.id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ deviceId, content })
  })
  if (!response.ok) {
    state.error = await response.text()
    return
  }
  cancelEditMessage()
}

async function deleteMessage(message) {
  if (!window.confirm('删除这条消息？')) return
  const response = await fetch(`/api/messages/${message.id}`, {
    method: 'DELETE'
  })
  if (!response.ok) {
    state.error = await response.text()
  }
}

async function createConversation() {
  const response = await fetch('/api/conversations', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title: `会话 ${state.conversations.length + 1}` })
  })
  const conversation = await response.json()
  activeConversationId.value = conversation.id
}

async function renameConversation(conversation) {
  const title = window.prompt('会话名称', conversation.title)
  if (!title || title.trim() === conversation.title) return
  const response = await fetch(`/api/conversations/${conversation.id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title })
  })
  if (!response.ok) state.error = await response.text()
}

async function deleteConversation(conversation) {
  if (!window.confirm(`删除「${conversation.title}」及其中消息？`)) return
  const response = await fetch(`/api/conversations/${conversation.id}`, { method: 'DELETE' })
  if (!response.ok) state.error = await response.text()
}

function onTextareaKeydown(event) {
  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
    event.preventDefault()
    sendMessage()
  }
}

function getOrCreateDeviceId() {
  let value = localStorage.getItem(STORAGE_DEVICE_ID)
  if (!value) {
    value = crypto.randomUUID?.() || `${Date.now()}-${Math.random()}`
    localStorage.setItem(STORAGE_DEVICE_ID, value)
  }
  return value
}

function defaultDeviceName() {
  const platform = platformName()
  return `${platform} 设备`
}

function platformName() {
  const ua = navigator.userAgent
  if (/iPhone|iPad|iPod/i.test(ua)) return 'iOS'
  if (/Android/i.test(ua)) return 'Android'
  if (/Windows/i.test(ua)) return 'Windows'
  if (/Macintosh/i.test(ua)) return 'macOS'
  if (/Linux/i.test(ua)) return 'Linux'
  return 'Browser'
}

function formatTime(value) {
  if (!value) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  }).format(new Date(value))
}

function isMine(message) {
  return message.deviceId === deviceId
}

function isEdited(message) {
  if (!message.updatedAt || !message.createdAt) return false
  return new Date(message.updatedAt).getTime() > new Date(message.createdAt).getTime()
}
</script>

<template>
  <main class="app-shell">
    <aside class="sidebar">
      <div class="brand">
        <div>
          <h1>EasySync</h1>
          <p>局域网文本同步</p>
        </div>
        <span class="status" :class="{ online: state.connected }">
          {{ state.connected ? '在线' : '重连中' }}
        </span>
      </div>

      <button class="primary-action" @click="createConversation">新建会话</button>

      <nav class="conversation-list">
        <button
          v-for="conversation in state.conversations"
          :key="conversation.id"
          class="conversation-item"
          :class="{ active: activeConversationId === conversation.id }"
          @click="activeConversationId = conversation.id"
        >
          <span>{{ conversation.title }}</span>
          <small>{{ formatTime(conversation.updatedAt) }}</small>
        </button>
      </nav>
    </aside>

    <section class="workspace">
      <header class="topbar">
        <div>
          <h2>{{ activeConversation?.title || '暂无会话' }}</h2>
          <p>{{ visibleMessages.length }} 条消息</p>
        </div>
        <div class="topbar-actions" v-if="activeConversation">
          <button @click="renameConversation(activeConversation)">重命名</button>
          <button class="danger" @click="deleteConversation(activeConversation)">删除</button>
        </div>
      </header>

      <div ref="messageList" class="message-list">
        <div v-if="visibleMessages.length === 0" class="empty">
          在下方输入文本，局域网内打开此页面的设备会实时看到。
        </div>
        <article
          v-for="message in visibleMessages"
          :key="message.id"
          class="message"
          :class="{ mine: isMine(message) }"
        >
          <div class="message-meta">
            <strong>{{ message.deviceName || '匿名设备' }}</strong>
            <span>{{ message.deviceType }}</span>
            <time>发送 {{ formatTime(message.createdAt) }}</time>
            <time v-if="isEdited(message)">编辑 {{ formatTime(message.updatedAt) }}</time>
          </div>
          <template v-if="editingMessageId === message.id">
            <textarea v-model="editingText" class="inline-editor" />
            <div class="message-actions">
              <button @click="saveEditMessage(message)">保存</button>
              <button @click="cancelEditMessage">取消</button>
            </div>
          </template>
          <template v-else>
            <p>{{ message.content }}</p>
            <div class="message-actions">
              <button class="copy" @click="copyMessage(message.content)">复制</button>
              <button v-if="isMine(message)" @click="startEditMessage(message)">编辑</button>
              <button class="danger" @click="deleteMessage(message)">删除</button>
            </div>
          </template>
        </article>
      </div>

      <footer class="composer">
        <textarea
          v-model="text"
          placeholder="输入文本，Ctrl/⌘ + Enter 发送"
          @keydown="onTextareaKeydown"
        />
        <div class="composer-actions">
          <button @click="pasteAndSend">粘贴并发送</button>
          <button class="send" :disabled="!text.trim() || !state.connected" @click="sendMessage">发送</button>
        </div>
      </footer>
    </section>

    <aside class="device-panel">
      <section>
        <h3>本机标识</h3>
        <label>
          <span>设备备注</span>
          <input v-model="deviceName" />
        </label>
        <p class="muted">{{ platformName() }} · {{ deviceId.slice(0, 8) }}</p>
      </section>

      <section>
        <h3>在线设备</h3>
        <div class="device-list">
          <div v-for="device in groupedPresence" :key="device.socket" class="device">
            <strong>{{ device.name || '匿名设备' }}</strong>
            <span>{{ device.type || 'Browser' }}</span>
          </div>
        </div>
      </section>

      <p v-if="state.error" class="error">{{ state.error }}</p>
    </aside>
  </main>
</template>
