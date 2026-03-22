import { ref, computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'

interface MatrixRoom {
  id: string
  alias: string
  name: string
  topic: string
  memberCount: number
}

export interface MatrixMessage {
  id: string
  senderName: string
  content: string
  timestamp: string
}

interface SendMessagePayload {
  roomId: string
  content: string
}

interface MessagesResponse {
  messages: MatrixMessage[]
  end: string
}

async function fetchRooms(): Promise<MatrixRoom[]> {
  const response = await fetch('/api/v1/forum/rooms', {
    headers: {
      'Authorization': `Bearer ${localStorage.getItem('access_token')}`,
    },
  })
  if (!response.ok) {
    throw new Error('Failed to fetch rooms')
  }
  return response.json()
}

async function fetchMessages(roomId: string, limit = 50, before?: string): Promise<MessagesResponse> {
  const params = new URLSearchParams({ limit: String(limit) })
  if (before) {
    params.set('before', before)
  }

  const response = await fetch(`/api/v1/forum/rooms/${roomId}/messages?${params}`, {
    headers: {
      'Authorization': `Bearer ${localStorage.getItem('access_token')}`,
    },
  })
  if (!response.ok) {
    throw new Error('Failed to fetch messages')
  }
  return response.json()
}

async function postMessage({ roomId, content }: SendMessagePayload): Promise<{ id: string }> {
  const response = await fetch(`/api/v1/forum/rooms/${roomId}/messages`, {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${localStorage.getItem('access_token')}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ content }),
  })
  if (!response.ok) {
    throw new Error('Failed to send message')
  }
  return response.json()
}

export function useMatrix() {
  const selectedRoomId = ref<string | null>(null)
  const queryClient = useQueryClient()

  const {
    data: rooms,
    isLoading: isLoadingRooms,
    error: roomsError,
  } = useQuery({
    queryKey: ['forum', 'rooms'],
    queryFn: fetchRooms,
    staleTime: 60 * 1000,
  })

  const isConnected = computed(() => !!rooms.value && rooms.value.length > 0)

  function useRoomMessages(roomId: () => string | null, limit = 50, before?: () => string | undefined) {
    return useQuery({
      queryKey: ['forum', 'messages', roomId, before],
      queryFn: () => {
        const id = roomId()
        if (!id) throw new Error('No room selected')
        return fetchMessages(id, limit, before?.())
      },
      enabled: () => !!roomId(),
      refetchInterval: 10 * 1000,
      staleTime: 5 * 1000,
    })
  }

  const { mutateAsync: sendMessage, isPending: isSending } = useMutation({
    mutationFn: postMessage,
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({
        queryKey: ['forum', 'messages', () => variables.roomId],
      })
    },
  })

  return {
    rooms,
    isLoadingRooms,
    roomsError,
    isConnected,
    selectedRoomId,
    useRoomMessages,
    sendMessage,
    isSending,
  }
}
