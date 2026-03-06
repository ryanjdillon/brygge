import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import FeatureRequestsView from '@/views/portal/FeatureRequestsView.vue'
import type { FeatureRequest } from '@/composables/useFeatureRequests'

const mockRequests: FeatureRequest[] = [
  {
    id: '1',
    club_id: 'c1',
    title: 'Dark mode',
    description: 'Add dark mode support',
    status: 'proposed',
    submitted_by: 'user1',
    vote_count: 5,
    user_vote: null,
    created_at: '2025-01-15T10:00:00Z',
    updated_at: '2025-01-15T10:00:00Z',
  },
  {
    id: '2',
    club_id: 'c1',
    title: 'Mobile app',
    description: 'Build a mobile app',
    status: 'reviewing',
    submitted_by: 'user2',
    vote_count: 3,
    user_vote: 1,
    created_at: '2025-01-16T10:00:00Z',
    updated_at: '2025-01-16T10:00:00Z',
  },
]

const mockMutate = vi.fn()
const mockVoteMutate = vi.fn()

vi.mock('@/composables/useFeatureRequests', () => ({
  useFeatureRequests: vi.fn(() => ({
    data: ref(mockRequests),
    isLoading: ref(false),
    isError: ref(false),
  })),
  useCreateFeatureRequest: vi.fn(() => ({
    mutate: mockMutate,
    isPending: ref(false),
  })),
  useVote: vi.fn(() => ({
    mutate: mockVoteMutate,
  })),
}))

vi.mock('lucide-vue-next', () => ({
  ThumbsUp: { template: '<span data-icon="thumbsup" />' },
  ThumbsDown: { template: '<span data-icon="thumbsdown" />' },
  Plus: { template: '<span data-icon="plus" />' },
  X: { template: '<span data-icon="x" />' },
}))

describe('FeatureRequestsView', () => {
  beforeEach(() => {
    mockMutate.mockReset()
    mockVoteMutate.mockReset()
  })

  it('renders feature request list', () => {
    const wrapper = mountWithPlugins(FeatureRequestsView)

    expect(wrapper.text()).toContain('Dark mode')
    expect(wrapper.text()).toContain('Mobile app')
    expect(wrapper.text()).toContain('Add dark mode support')
  })

  it('renders vote counts', () => {
    const wrapper = mountWithPlugins(FeatureRequestsView)
    expect(wrapper.text()).toContain('5')
    expect(wrapper.text()).toContain('3')
  })

  it('upvote button triggers vote mutation', async () => {
    const wrapper = mountWithPlugins(FeatureRequestsView)

    const upvoteButtons = wrapper.findAll('[data-icon="thumbsup"]')
    expect(upvoteButtons.length).toBeGreaterThan(0)

    await upvoteButtons[0].element.parentElement!.click()

    expect(mockVoteMutate).toHaveBeenCalledWith({ requestId: '1', value: 1 })
  })

  it('downvote button triggers vote mutation', async () => {
    const wrapper = mountWithPlugins(FeatureRequestsView)

    const downvoteButtons = wrapper.findAll('[data-icon="thumbsdown"]')
    expect(downvoteButtons.length).toBeGreaterThan(0)

    await downvoteButtons[0].element.parentElement!.click()

    expect(mockVoteMutate).toHaveBeenCalledWith({ requestId: '1', value: -1 })
  })

  it('status filter buttons are rendered', () => {
    const wrapper = mountWithPlugins(FeatureRequestsView)

    expect(wrapper.text()).toContain('featureRequests.filterAll')
    expect(wrapper.text()).toContain('featureRequests.statusProposed')
    expect(wrapper.text()).toContain('featureRequests.statusReviewing')
    expect(wrapper.text()).toContain('featureRequests.statusAccepted')
    expect(wrapper.text()).toContain('featureRequests.statusDone')
  })

  it('status filter changes on click', async () => {
    const wrapper = mountWithPlugins(FeatureRequestsView)

    const filterButtons = wrapper.findAll('.rounded-full')
    const proposedBtn = filterButtons.find((btn) => btn.text().includes('featureRequests.statusProposed'))

    expect(proposedBtn).toBeDefined()
    await proposedBtn!.trigger('click')

    expect(proposedBtn!.classes()).toContain('bg-blue-600')
  })

  it('create request modal opens on button click', async () => {
    const wrapper = mountWithPlugins(FeatureRequestsView)

    expect(wrapper.find('.fixed').exists()).toBe(false)

    const submitButton = wrapper.find('button.bg-blue-600')
    await submitButton.trigger('click')

    expect(wrapper.find('.fixed').exists()).toBe(true)
    expect(wrapper.text()).toContain('featureRequests.submitNew')
  })

  it('modal form triggers create mutation on submit', async () => {
    const wrapper = mountWithPlugins(FeatureRequestsView)

    const submitButton = wrapper.find('button.bg-blue-600')
    await submitButton.trigger('click')

    const modal = wrapper.find('.fixed')
    const titleInput = modal.find('input[type="text"]')
    const textarea = modal.find('textarea')

    await titleInput.setValue('New feature')
    await textarea.setValue('Description of new feature')

    await modal.find('form').trigger('submit')

    expect(mockMutate).toHaveBeenCalledWith(
      { title: 'New feature', description: 'Description of new feature' },
      expect.any(Object),
    )
  })
})
