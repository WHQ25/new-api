package relay

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaykitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cascading new-api settles video tasks on the token count reported by
// /v1/videos/{id}. Per-provider ConvertToOpenAIVideo output carries no usage, so
// the response builder has to republish it from the stored upstream payload.
func TestAttachVideoTokenUsage(t *testing.T) {
	baseVideo := func() []byte {
		video := relaykitdto.NewOpenAIVideo()
		video.ID = "task-1"
		video.Status = relaykitdto.VideoStatusCompleted
		encoded, err := common.Marshal(video)
		require.NoError(t, err)
		return encoded
	}

	videoWithUsage := func(totalTokens int) []byte {
		video := relaykitdto.NewOpenAIVideo()
		video.ID = "task-1"
		video.Status = relaykitdto.VideoStatusCompleted
		video.Usage = &relaykitdto.OpenAIVideoUsage{TotalTokens: totalTokens}
		encoded, err := common.Marshal(video)
		require.NoError(t, err)
		return encoded
	}

	cases := []struct {
		name      string
		taskData  string
		videoBody []byte
		want      int
	}{
		{
			name:      "top-level usage from the provider payload",
			taskData:  `{"usage":{"total_tokens":243000}}`,
			videoBody: baseVideo(),
			want:      243000,
		},
		{
			name:      "nested data usage from the provider payload",
			taskData:  `{"data":{"usage":{"total_tokens":51200}}}`,
			videoBody: baseVideo(),
			want:      51200,
		},
		{
			// A cascaded converter that already carries usage owns the number;
			// the stored payload must not override it.
			name:      "usage from the converter is preserved",
			taskData:  `{"usage":{"total_tokens":700}}`,
			videoBody: videoWithUsage(512),
			want:      512,
		},
		{
			name:      "no usage reported leaves the response untouched",
			taskData:  `{"status":"succeeded"}`,
			videoBody: baseVideo(),
			want:      0,
		},
		{
			// Republishing must not launder the anomaly into the billing
			// ceiling: a cascading downstream needs the out-of-range number so
			// it can clamp and audit it itself.
			name:      "absurd upstream token count is republished, not laundered",
			taskData:  `{"usage":{"total_tokens":99999999999999}}`,
			videoBody: baseVideo(),
			want:      math.MaxInt32,
		},
		{
			name:      "negative token count is ignored",
			taskData:  `{"usage":{"total_tokens":-5}}`,
			videoBody: baseVideo(),
			want:      0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{Data: []byte(tc.taskData)}

			merged := attachVideoTokenUsage(tc.videoBody, task)

			var video relaykitdto.OpenAIVideo
			require.NoError(t, common.Unmarshal(merged, &video))
			if tc.want == 0 {
				assert.Nil(t, video.Usage)
			} else {
				require.NotNil(t, video.Usage)
				assert.Equal(t, tc.want, video.Usage.TotalTokens)
			}
			// Injecting usage must not disturb the rest of the response.
			assert.Equal(t, "task-1", video.ID)
			assert.Equal(t, relaykitdto.VideoStatusCompleted, video.Status)
		})
	}
}
