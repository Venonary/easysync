<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'

const STORAGE_DEVICE_ID = 'easysync.deviceId'
const STORAGE_DEVICE_NAME = 'easysync.deviceName'
const STORAGE_ACTIVE_CONVERSATION = 'easysync.activeConversationId'
const STORAGE_THEME = 'easysync.theme'

const themeOptions = [
  { value: 'light', label: '浅色主题' },
  { value: 'dark', label: '深色主题' },
  { value: 'system', label: '跟随系统' }
]

const state = reactive({
  conversations: [],
  messages: [],
  presence: [],
  connected: false,
  error: ''
})

const text = ref('')
const activeConversationId = ref(safeStorageGet(STORAGE_ACTIVE_CONVERSATION))
const deviceId = getOrCreateDeviceId()
const deviceName = ref(safeStorageGet(STORAGE_DEVICE_NAME) || defaultDeviceName())
const themeMode = ref(safeStorageGet(STORAGE_THEME) || 'system')
const systemPrefersDark = ref(window.matchMedia('(prefers-color-scheme: dark)').matches)
const socket = ref(null)
const messageList = ref(null)
const composerTextarea = ref(null)
const imageInput = ref(null)
const fileInput = ref(null)
const editingMessageId = ref('')
const editingText = ref('')
const uploadHint = ref('可粘贴图片，或从文件/相册中选择上传')
const uploading = ref(false)
const globalPasteHandler = (event) => {
  const target = event.target
  const tagName = target?.tagName?.toLowerCase()
  if (tagName === 'textarea' || tagName === 'input') return
  handlePaste(event)
}
let mediaQueryList = null

const handleSystemThemeChange = (event) => {
  systemPrefersDark.value = event.matches
}

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

const effectiveTheme = computed(() => {
  if (themeMode.value === 'system') {
    return systemPrefersDark.value ? 'dark' : 'light'
  }
  return themeMode.value
})

const canCopyImage = computed(() => {
  return Boolean(window.isSecureContext && navigator.clipboard?.write && typeof ClipboardItem !== 'undefined')
})

watch(deviceName, (value) => {
  safeStorageSet(STORAGE_DEVICE_NAME, value.trim() || defaultDeviceName())
})

watch(activeConversationId, (value) => {
  if (value) safeStorageSet(STORAGE_ACTIVE_CONVERSATION, value)
})

watch(themeMode, (value) => {
  safeStorageSet(STORAGE_THEME, value)
})

watch(effectiveTheme, (value) => {
  document.documentElement.dataset.theme = value
  document.documentElement.style.colorScheme = value
}, { immediate: true })

watch(visibleMessages, async () => {
  await nextTick()
  if (messageList.value) {
    messageList.value.scrollTop = messageList.value.scrollHeight
  }
})

onMounted(async () => {
  mediaQueryList = window.matchMedia('(prefers-color-scheme: dark)')
  systemPrefersDark.value = mediaQueryList.matches
  if (mediaQueryList.addEventListener) {
    mediaQueryList.addEventListener('change', handleSystemThemeChange)
  } else if (mediaQueryList.addListener) {
    mediaQueryList.addListener(handleSystemThemeChange)
  }
  window.addEventListener('paste', globalPasteHandler)
  await loadState()
  connect()
})

onBeforeUnmount(() => {
  if (mediaQueryList) {
    if (mediaQueryList.removeEventListener) {
      mediaQueryList.removeEventListener('change', handleSystemThemeChange)
    } else if (mediaQueryList.removeListener) {
      mediaQueryList.removeListener(handleSystemThemeChange)
    }
  }
  window.removeEventListener('paste', globalPasteHandler)
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

async function handlePaste(event) {
  const clipboardData = event.clipboardData
  const file = pickImageFile(clipboardData)
  if (file) {
    event.preventDefault()
    if (socket.value?.readyState !== WebSocket.OPEN || !activeConversation.value) {
      state.error = '当前未连接到服务端，无法发送图片'
      return
    }

    try {
      await uploadFile(file)
      uploadHint.value = '图片已发送'
      state.error = ''
    } catch (err) {
      state.error = '图片上传失败，请尝试更小的图片'
      uploadHint.value = '图片发送失败'
    }
    return
  }

  const pastedText = extractClipboardText(clipboardData)
  if (pastedText) {
    event.preventDefault()
    insertTextIntoComposer(pastedText)
    state.error = ''
    return
  }

  const clipboardTypes = Array.from(clipboardData?.types || [])
  if (clipboardTypes.some((item) => item === 'Files' || item.startsWith('image/'))) {
    state.error = '检测到剪贴板内容，但未能读取图片文件'
  }
}

function pickImageFile(clipboardData) {
  const item = Array.from(clipboardData?.items || []).find((entry) => entry.type.startsWith('image/'))
  if (item) {
    return item.getAsFile()
  }

  const file = Array.from(clipboardData?.files || []).find((entry) => entry.type.startsWith('image/'))
  if (file) {
    return file
  }

  return null
}

function extractClipboardText(clipboardData) {
  if (!clipboardData?.getData) return ''

  const plainText = clipboardData.getData('text/plain').trim()
  if (plainText) return plainText

  const uriList = normalizeUriList(clipboardData.getData('text/uri-list'))
  if (uriList) return uriList

  const htmlText = htmlToPlainText(clipboardData.getData('text/html'))
  if (htmlText) return htmlText

  return ''
}

function normalizeUriList(value) {
  return String(value || '')
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter((item) => item && !item.startsWith('#'))
    .join('\n')
}

function htmlToPlainText(html) {
  if (!html) return ''
  const container = document.createElement('div')
  container.innerHTML = html
  return (container.innerText || container.textContent || '').trim()
}

function insertTextIntoComposer(value) {
  const target = composerTextarea.value
  if (!target) {
    text.value += value
    return
  }

  const start = target.selectionStart ?? text.value.length
  const end = target.selectionEnd ?? text.value.length
  const nextText = `${text.value.slice(0, start)}${value}${text.value.slice(end)}`
  text.value = nextText

  nextTick(() => {
    const cursor = start + value.length
    target.focus()
    target.setSelectionRange(cursor, cursor)
  })
}

function chooseFile() {
  fileInput.value?.click()
}

function chooseImage() {
  imageInput.value?.click()
}

async function handleFileSelected(event) {
  const files = Array.from(event.target.files || [])
  event.target.value = ''
  for (const file of files) {
    await uploadFile(file)
  }
}

async function uploadFile(file) {
  if (!file || !activeConversation.value) return
  const formData = new FormData()
  formData.append('conversationId', activeConversation.value.id)
  formData.append('deviceId', deviceId)
  formData.append('deviceName', deviceName.value)
  formData.append('deviceType', platformName())
  formData.append('messageType', file.type.startsWith('image/') ? 'image' : 'file')
  formData.append('file', file)

  uploading.value = true
  uploadHint.value = `正在上传 ${file.name || '文件'}`
  try {
    const response = await fetch('/api/uploads', {
      method: 'POST',
      body: formData
    })
    if (!response.ok) {
      throw new Error(await response.text())
    }
    state.error = ''
    uploadHint.value = `${file.name || '文件'} 已上传`
  } catch (err) {
    state.error = err?.message || '文件上传失败'
    uploadHint.value = '文件上传失败'
  } finally {
    uploading.value = false
  }
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
    state.error = ''
  } catch {
    if (fallbackCopyText(content)) {
      state.error = ''
      return
    }
    state.error = '复制失败，请手动选择文本复制'
  }
}

function fallbackCopyText(content) {
  try {
    const textarea = document.createElement('textarea')
    textarea.value = content
    textarea.setAttribute('readonly', 'true')
    textarea.style.position = 'fixed'
    textarea.style.left = '-9999px'
    textarea.style.top = '0'
    document.body.appendChild(textarea)
    textarea.select()
    const success = document.execCommand('copy')
    document.body.removeChild(textarea)
    return success
  } catch {
    return false
  }
}

async function copyImageMessage(message) {
  try {
    const blob = await fetchAttachmentBlob(message)
    const copied = await tryCopyBlobAsImage(blob)
    if (copied) {
      state.error = ''
      uploadHint.value = '图片已复制到剪贴板'
      return
    }
  }

  catch {
    // fall through to the visible error below
  }

  state.error = '当前环境不支持直接复制图片，请使用保存按钮'
  uploadHint.value = '图片复制失败'
}

async function saveImageMessage(message) {
  const blob = await fetchAttachmentBlob(message)
  const fileName = attachmentName(message) || `easysync-${message.id || Date.now()}.png`
  saveBlob(blob, fileName)
  state.error = ''
  uploadHint.value = '图片已保存'
}

async function tryCopyBlobAsImage(blob) {
  if (!navigator.clipboard?.write || typeof ClipboardItem === 'undefined') {
    return false
  }

  try {
    await navigator.clipboard.write([
      new ClipboardItem({ [blob.type || 'image/png']: blob })
    ])
    return true
  } catch {
    return false
  }
}

async function fetchAttachmentBlob(message) {
  const response = await fetch(attachmentUrl(message))
  return await response.blob()
}

function saveBlob(blob, fileName) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = fileName
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
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
  const title = nextConversationTitle()
  const response = await fetch('/api/conversations', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title })
  })
  const conversation = await response.json()
  activeConversationId.value = conversation.id
}

function nextConversationTitle() {
  const usedNumbers = new Set()
  for (const conversation of state.conversations) {
    const match = /^会话\s*(\d+)$/.exec(conversation.title)
    if (match) {
      usedNumbers.add(Number(match[1]))
    }
  }

  let index = 1
  while (usedNumbers.has(index)) {
    index += 1
  }
  return `会话 ${index}`
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
  let value = safeStorageGet(STORAGE_DEVICE_ID)
  if (!value) {
    value = crypto.randomUUID?.() || `${Date.now()}-${Math.random()}`
    safeStorageSet(STORAGE_DEVICE_ID, value)
  }
  return value
}

function safeStorageGet(key) {
  try {
    return localStorage.getItem(key) || ''
  } catch {
    return ''
  }
}

function safeStorageSet(key, value) {
  try {
    localStorage.setItem(key, String(value))
  } catch {
    // Ignore storage failures so the app can still render and function.
  }
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

function isImageMessage(message) {
  return message.type === 'image' || /^data:image\//.test(message.content || '') || (message.attachment?.mimeType || '').startsWith('image/')
}

function isFileMessage(message) {
  return message.type === 'file' || Boolean(message.attachment)
}

function attachmentUrl(message) {
  return message.attachment?.url || message.content || ''
}

function attachmentName(message) {
  return message.attachment?.name || message.content || '文件'
}

function formatFileSize(size) {
  if (!size) return ''
  const units = ['B', 'KB', 'MB', 'GB']
  let value = Number(size)
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex += 1
  }
  return `${value.toFixed(value >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`
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
        <div
          v-for="conversation in state.conversations"
          :key="conversation.id"
          class="conversation-item"
          :class="{ active: activeConversationId === conversation.id }"
        >
          <div class="conversation-select" @click="activeConversationId = conversation.id">
            <span class="conversation-title-row">
              <span class="conversation-title">{{ conversation.title }}</span>
              <button
                class="conversation-pencil"
                type="button"
                :aria-label="`重命名 ${conversation.title}`"
                title="重命名会话"
                @click.stop="renameConversation(conversation)"
              >
                ✎
              </button>
              <button
                class="conversation-delete"
                type="button"
                :aria-label="`删除 ${conversation.title}`"
                title="删除会话"
                @click.stop="deleteConversation(conversation)"
              >
                ×
              </button>
            </span>
            <small>{{ formatTime(conversation.updatedAt) }}</small>
          </div>
        </div>
      </nav>
    </aside>

    <section class="workspace">
      <header class="topbar">
        <div>
          <button
            v-if="activeConversation"
            class="topbar-title-action"
            type="button"
            @click="renameConversation(activeConversation)"
            :aria-label="`重命名 ${activeConversation.title}`"
            title="点击标题改名"
          >
            <h2>{{ activeConversation.title }}</h2>
            <span class="topbar-pencil" aria-hidden="true">✎</span>
          </button>
          <h2 v-else>暂无会话</h2>
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
            <img v-if="isImageMessage(message)" :src="attachmentUrl(message)" class="message-image" :alt="attachmentName(message)" />
            <a v-else-if="isFileMessage(message)" class="file-card" :href="attachmentUrl(message)" :download="attachmentName(message)">
              <span class="file-icon" aria-hidden="true">⬇</span>
              <span class="file-info">
                <strong>{{ attachmentName(message) }}</strong>
                <small>{{ message.attachment?.mimeType || '文件' }} {{ formatFileSize(message.attachment?.size) }}</small>
              </span>
            </a>
            <p v-else>{{ message.content }}</p>
            <div class="message-actions">
              <template v-if="isImageMessage(message)">
                <button v-if="canCopyImage" class="copy" @click="copyImageMessage(message)">复制图片</button>
                <button class="copy" @click="saveImageMessage(message)">保存图片</button>
                <a class="button-link" :href="attachmentUrl(message)" :download="attachmentName(message)">下载</a>
              </template>
              <template v-else-if="isFileMessage(message)">
                <a class="button-link" :href="attachmentUrl(message)" :download="attachmentName(message)">下载</a>
              </template>
              <button v-else class="copy" @click="copyMessage(message.content)">复制</button>
              <button v-if="isMine(message) && message.type === 'text'" @click="startEditMessage(message)">编辑</button>
              <button class="danger" @click="deleteMessage(message)">删除</button>
            </div>
          </template>
        </article>
      </div>

      <footer class="composer">
        <textarea
          ref="composerTextarea"
          v-model="text"
          placeholder="输入文本，Ctrl/⌘ + Enter 发送"
          @keydown="onTextareaKeydown"
          @paste="handlePaste"
        />
        <div class="composer-actions">
          <input ref="imageInput" class="file-input" type="file" multiple accept="image/*" @change="handleFileSelected" />
          <input ref="fileInput" class="file-input" type="file" multiple @change="handleFileSelected" />
          <button :disabled="uploading || !activeConversation" @click="chooseImage">
            {{ uploading ? '上传中' : '上传图片' }}
          </button>
          <button :disabled="uploading || !activeConversation" @click="chooseFile">
            {{ uploading ? '上传中' : '上传文件' }}
          </button>
          <button @click="pasteAndSend">粘贴并发送</button>
          <button class="send" :disabled="!text.trim() || !state.connected" @click="sendMessage">发送</button>
        </div>
        <p class="muted composer-hint">{{ uploadHint }}</p>
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
        <h3>视觉主题</h3>
        <label>
          <span>主题模式</span>
          <select v-model="themeMode">
            <option v-for="option in themeOptions" :key="option.value" :value="option.value">
              {{ option.label }}
            </option>
          </select>
        </label>
        <p class="muted">当前：{{ effectiveTheme === 'dark' ? '深色' : '浅色' }}</p>
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
